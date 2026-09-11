package collector

import (
	"testing"
	"time"
)

func TestToSnapshotContenedorEnMarcha(t *testing.T) {
	k := &KubeletInspector{cpuCores: 4}
	startedAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	pod := kubePod{
		Metadata: kubeObjectMeta{Name: "web-abc123", Namespace: "prod"},
	}
	cs := kubeContainerStatus{
		Name:         "web",
		Image:        "nginx:1.27",
		RestartCount: 2,
		State:        kubeContainerState{Running: &kubeStateRunning{StartedAt: startedAt}},
	}
	usage := map[podContainerKey]kubeContainerStats{
		{namespace: "prod", pod: "web-abc123", container: "web"}: {
			CPU:    kubeCPUStats{UsageNanoCores: 2_000_000_000}, // 2 nucleos completos
			Memory: kubeMemoryStats{WorkingSetBytes: 500 * 1024 * 1024},
		},
	}

	snap := k.toSnapshot(pod, cs, usage)

	if snap.ID != "prod/web-abc123/web" {
		t.Errorf("ID = %q", snap.ID)
	}
	if snap.Name != "web-abc123/web" {
		t.Errorf("Name = %q", snap.Name)
	}
	if snap.State != "running" {
		t.Errorf("State = %q", snap.State)
	}
	if snap.RestartCount != 2 {
		t.Errorf("RestartCount = %d", snap.RestartCount)
	}
	if !snap.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt = %v", snap.StartedAt)
	}
	// 2 nucleos de uso sobre 4 nucleos totales = 50%.
	if got, want := snap.CPUPercent, 50.0; got != want {
		t.Errorf("CPUPercent = %v, se esperaba %v", got, want)
	}
	if snap.MemoryBytes != 500*1024*1024 {
		t.Errorf("MemoryBytes = %d", snap.MemoryBytes)
	}
	if snap.Labels["namespace"] != "prod" || snap.Labels["pod"] != "web-abc123" {
		t.Errorf("Labels = %+v", snap.Labels)
	}
}

func TestToSnapshotEstados(t *testing.T) {
	k := &KubeletInspector{cpuCores: 4}
	pod := kubePod{Metadata: kubeObjectMeta{Name: "p", Namespace: "ns"}}
	usage := map[podContainerKey]kubeContainerStats{}

	waiting := k.toSnapshot(pod, kubeContainerStatus{
		Name:  "c",
		State: kubeContainerState{Waiting: &kubeStateWaiting{Reason: "ImagePullBackOff"}},
	}, usage)
	if waiting.State != "waiting" || waiting.Status != "Waiting: ImagePullBackOff" {
		t.Errorf("waiting = %+v", waiting)
	}

	terminated := k.toSnapshot(pod, kubeContainerStatus{
		Name:  "c",
		State: kubeContainerState{Terminated: &kubeStateTerminated{Reason: "Error", ExitCode: 137}},
	}, usage)
	if terminated.State != "exited" || terminated.Status != "Exited (137)" {
		t.Errorf("terminated = %+v", terminated)
	}
}

func TestIndexContainerStats(t *testing.T) {
	stats := summaryStats{
		Pods: []kubePodStats{
			{
				PodRef: kubePodRef{Name: "p1", Namespace: "ns1"},
				Containers: []kubeContainerStats{
					{Name: "c1", CPU: kubeCPUStats{UsageNanoCores: 100}},
					{Name: "c2", CPU: kubeCPUStats{UsageNanoCores: 200}},
				},
			},
		},
	}

	index := indexContainerStats(stats)
	if len(index) != 2 {
		t.Fatalf("len(index) = %d, se esperaban 2", len(index))
	}
	if index[podContainerKey{namespace: "ns1", pod: "p1", container: "c1"}].CPU.UsageNanoCores != 100 {
		t.Error("c1 no indexado correctamente")
	}
}

func TestToSnapshotSinMetricasNoFalla(t *testing.T) {
	k := &KubeletInspector{cpuCores: 0} // division por cero evitada explicitamente
	pod := kubePod{Metadata: kubeObjectMeta{Name: "p", Namespace: "ns"}}
	cs := kubeContainerStatus{Name: "c", State: kubeContainerState{Running: &kubeStateRunning{}}}

	snap := k.toSnapshot(pod, cs, map[podContainerKey]kubeContainerStats{})
	if snap.CPUPercent != 0 {
		t.Errorf("CPUPercent = %v, se esperaba 0 sin nucleos conocidos", snap.CPUPercent)
	}
}
