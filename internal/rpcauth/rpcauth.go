// Package rpcauth centraliza la autenticacion por token compartido entre el
// agente y el servidor: un unico lugar que define como viaja el token en los
// metadatos gRPC, para que cliente y servidor nunca se desincronicen.
package rpcauth

import (
	"context"
	"crypto/subtle"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// metadataKey es la cabecera gRPC donde viaja el AGENT_TOKEN en cada llamada.
// No se reutiliza el campo `token` de RegisterRequest para las demas RPCs
// (PushMetrics, StreamCommands...) porque son streams de larga duracion y la
// autenticacion por metadatos cubre las cuatro con el mismo interceptor.
const metadataKey = "authorization"

// ErrMissingToken se devuelve cuando la llamada no trae cabecera de token.
var ErrMissingToken = errors.New("rpcauth: falta el token en los metadatos")

// ErrInvalidToken se devuelve cuando el token no coincide con ninguno valido.
var ErrInvalidToken = errors.New("rpcauth: token invalido")

// OutgoingContext adjunta el token al contexto saliente del cliente gRPC.
func OutgoingContext(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, metadataKey, token)
}

// TokenFromIncoming extrae el token de una llamada entrante.
func TokenFromIncoming(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ErrMissingToken
	}
	values := md.Get(metadataKey)
	if len(values) == 0 || values[0] == "" {
		return "", ErrMissingToken
	}
	return values[0], nil
}

// Validator decide si un token es valido. El servidor la implementa
// comparando contra la lista de tokens configurados.
type Validator func(token string) bool

// StaticValidator construye un Validator a partir de una lista fija de
// tokens validos, comparando en tiempo constante para no filtrar el token
// correcto por temporizacion.
func StaticValidator(tokens []string) Validator {
	return func(candidate string) bool {
		for _, t := range tokens {
			if subtle.ConstantTimeCompare([]byte(t), []byte(candidate)) == 1 {
				return true
			}
		}
		return false
	}
}

// UnaryServerInterceptor rechaza las llamadas unarias sin un token valido.
func UnaryServerInterceptor(validate Validator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := authenticate(ctx, validate); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor rechaza los streams sin un token valido.
func StreamServerInterceptor(validate Validator) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := authenticate(ss.Context(), validate); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func authenticate(ctx context.Context, validate Validator) error {
	token, err := TokenFromIncoming(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}
	if !validate(token) {
		return status.Error(codes.Unauthenticated, ErrInvalidToken.Error())
	}
	return nil
}
