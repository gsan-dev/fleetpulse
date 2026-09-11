package collector

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Rutas estandar que Kubernetes proyecta en todo pod con una ServiceAccount:
// el token se renueva por el kubelet sin que el agente tenga que hacer nada.
const (
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubeletPort             = "10250"
)

// KubeletInspector obtiene los contenedores (en Kubernetes, "contenedores
// dentro de Pods") de este nodo hablando directamente con la API HTTPS del
// kubelet local, sin pasar por el API server ni depender de client-go: dos
// peticiones HTTP y JSON minimo bastan, lo que mantiene el binario del
// agente igual de ligero en un DaemonSet que en un host normal.
//
// Requiere permisos RBAC sobre "nodes/proxy" y "nodes/stats" para la
// ServiceAccount del DaemonSet (ver deploy/helm/fleetpulse-agent).
type KubeletInspector struct {
	baseURL  string
	token    string
	client   *http.Client
	nodeName string
	cpuCores float64
}

// NewKubeletInspector construye el inspector a partir del entorno que
// Kubernetes proyecta en el Pod. `nodeIP` debe llegar via la Downward API
// (campo status.hostIP) porque el kubelet solo escucha en la IP del nodo, no
// en localhost dentro del contenedor. `cpuCores` se usa para expresar el uso
// de CPU como porcentaje del nodo, igual que hace el inspector de Docker.
func NewKubeletInspector(nodeIP, nodeName string, cpuCores float64) (*KubeletInspector, error) {
	if nodeIP == "" {
		return nil, fmt.Errorf("kubelet: falta NODE_IP (¿el DaemonSet no proyecta status.hostIP?)")
	}

	token, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return nil, fmt.Errorf("kubelet: leer token de la ServiceAccount: %w", err)
	}

	return &KubeletInspector{
		baseURL:  fmt.Sprintf("https://%s:%s", nodeIP, kubeletPort),
		token:    strings.TrimSpace(string(token)),
		nodeName: nodeName,
		cpuCores: cpuCores,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				// El certificado que sirve el kubelet es el del nodo, no uno
				// firmado por la CA del cluster: verificarlo exigiria
				// distribuir esa CA aparte. La autenticacion real la hace el
				// token de la ServiceAccount (Bearer), que el kubelet valida
				// contra el API server via TokenReview/SubjectAccessReview;
				// esto es lo que hacen la mayoria de los recolectores
				// ligeros de metricas de kubelet.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		},
	}, nil
}

func (k *KubeletInspector) Close() error { return nil }

func (k *KubeletInspector) Containers(ctx context.Context) ([]ContainerSnapshot, error) {
	pods, err := k.fetchPods(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := k.fetchStatsSummary(ctx)
	if err != nil {
		// Sin metricas de recursos el listado sigue siendo util (estado,
		// reinicios): se degrada en vez de fallar del todo.
		stats = summaryStats{}
	}

	usage := indexContainerStats(stats)
	snapshots := make([]ContainerSnapshot, 0)
	for _, pod := range pods.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			snapshots = append(snapshots, k.toSnapshot(pod, cs, usage))
		}
	}
	return snapshots, nil
}

func (k *KubeletInspector) toSnapshot(pod kubePod, cs kubeContainerStatus, usage map[podContainerKey]kubeContainerStats) ContainerSnapshot {
	// El id combina namespace/pod/contenedor: en Docker el ID ya es unico de
	// por si, pero en Kubernetes dos pods distintos pueden tener contenedores
	// con el mismo nombre, y el panel usa este campo como clave.
	id := fmt.Sprintf("%s/%s/%s", pod.Metadata.Namespace, pod.Metadata.Name, cs.Name)
	snap := ContainerSnapshot{
		ID:           id,
		Name:         fmt.Sprintf("%s/%s", pod.Metadata.Name, cs.Name),
		Image:        cs.Image,
		RestartCount: cs.RestartCount,
		Labels:       map[string]string{"namespace": pod.Metadata.Namespace, "pod": pod.Metadata.Name},
	}

	switch {
	case cs.State.Running != nil:
		snap.State = "running"
		snap.Status = "Up"
		snap.StartedAt = cs.State.Running.StartedAt
	case cs.State.Waiting != nil:
		snap.State = "waiting"
		snap.Status = "Waiting: " + cs.State.Waiting.Reason
	case cs.State.Terminated != nil:
		snap.State = "exited"
		snap.Status = fmt.Sprintf("Exited (%d)", cs.State.Terminated.ExitCode)
	default:
		snap.State = "unknown"
	}

	if stat, ok := usage[podContainerKey{namespace: pod.Metadata.Namespace, pod: pod.Metadata.Name, container: cs.Name}]; ok {
		if k.cpuCores > 0 {
			snap.CPUPercent = (float64(stat.CPU.UsageNanoCores) / 1e9) / k.cpuCores * 100
		}
		snap.MemoryBytes = stat.Memory.WorkingSetBytes
	}

	return snap
}

func (k *KubeletInspector) fetchPods(ctx context.Context) (podList, error) {
	var out podList
	if err := k.getJSON(ctx, "/pods", &out); err != nil {
		return podList{}, fmt.Errorf("kubelet: listar pods: %w", err)
	}
	return out, nil
}

func (k *KubeletInspector) fetchStatsSummary(ctx context.Context) (summaryStats, error) {
	var out summaryStats
	if err := k.getJSON(ctx, "/stats/summary", &out); err != nil {
		return summaryStats{}, fmt.Errorf("kubelet: leer stats/summary: %w", err)
	}
	return out, nil
}

func (k *KubeletInspector) getJSON(ctx context.Context, path string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	req.Header.Set("Accept", "application/json")

	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kubelet respondio %d: %s", resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

// --- Tipos minimos del API del kubelet -------------------------------------
//
// Se declaran a mano (en vez de importar k8s.io/api + k8s.io/apimachinery)
// para no arrastrar esas dependencias, bastante pesadas, a un agente pensado
// para pesar poco. Solo se listan los campos que el agente usa de verdad.

type podList struct {
	Items []kubePod `json:"items"`
}

type kubePod struct {
	Metadata kubeObjectMeta `json:"metadata"`
	Status   kubePodStatus  `json:"status"`
}

type kubeObjectMeta struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type kubePodStatus struct {
	ContainerStatuses []kubeContainerStatus `json:"containerStatuses"`
}

type kubeContainerStatus struct {
	Name         string             `json:"name"`
	Image        string             `json:"image"`
	RestartCount int                `json:"restartCount"`
	State        kubeContainerState `json:"state"`
}

type kubeContainerState struct {
	Running    *kubeStateRunning    `json:"running,omitempty"`
	Waiting    *kubeStateWaiting    `json:"waiting,omitempty"`
	Terminated *kubeStateTerminated `json:"terminated,omitempty"`
}

type kubeStateRunning struct {
	StartedAt time.Time `json:"startedAt"`
}

type kubeStateWaiting struct {
	Reason string `json:"reason"`
}

type kubeStateTerminated struct {
	Reason   string `json:"reason"`
	ExitCode int    `json:"exitCode"`
}

type summaryStats struct {
	Pods []kubePodStats `json:"pods"`
}

type kubePodStats struct {
	PodRef     kubePodRef           `json:"podRef"`
	Containers []kubeContainerStats `json:"containers"`
}

type kubePodRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type kubeContainerStats struct {
	Name   string          `json:"name"`
	CPU    kubeCPUStats    `json:"cpu"`
	Memory kubeMemoryStats `json:"memory"`
}

type kubeCPUStats struct {
	UsageNanoCores uint64 `json:"usageNanoCores"`
}

type kubeMemoryStats struct {
	WorkingSetBytes uint64 `json:"workingSetBytes"`
}

type podContainerKey struct {
	namespace string
	pod       string
	container string
}

// indexContainerStats aplana stats/summary (agrupado por pod) a un mapa
// indexado por namespace/pod/contenedor para cruzarlo en O(1) contra /pods.
func indexContainerStats(stats summaryStats) map[podContainerKey]kubeContainerStats {
	index := make(map[podContainerKey]kubeContainerStats)
	for _, pod := range stats.Pods {
		for _, c := range pod.Containers {
			key := podContainerKey{namespace: pod.PodRef.Namespace, pod: pod.PodRef.Name, container: c.Name}
			index[key] = c
		}
	}
	return index
}
