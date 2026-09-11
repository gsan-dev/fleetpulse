package rpcauth

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// outgoingToIncoming simula lo que hace gRPC al enviar una llamada: los
// metadatos que el cliente adjunto como salientes (via OutgoingContext) se
// convierten en entrantes al otro lado, que es lo que TokenFromIncoming espera.
func outgoingToIncoming(ctx context.Context) (context.Context, bool) {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return ctx, false
	}
	return metadata.NewIncomingContext(ctx, md), true
}

func TestStaticValidator(t *testing.T) {
	validate := StaticValidator([]string{"token-a", "token-b"})

	if !validate("token-a") {
		t.Error("token-a deberia ser valido")
	}
	if validate("token-c") {
		t.Error("token-c no deberia ser valido")
	}
	if validate("") {
		t.Error("el token vacio no deberia ser valido")
	}
}

func TestTokenFromIncomingSinMetadatos(t *testing.T) {
	if _, err := TokenFromIncoming(context.Background()); err != ErrMissingToken {
		t.Errorf("err = %v, se esperaba ErrMissingToken", err)
	}
}

func TestOutgoingContextIdaYVuelta(t *testing.T) {
	ctx := OutgoingContext(context.Background(), "mi-token")
	// OutgoingContext deja el token en los metadatos SALIENTES; el helper de
	// lectura espera metadatos ENTRANTES, que es lo que ve el servidor. Se
	// simula convirtiendo el contexto como lo haria gRPC en el otro extremo.
	incomingCtx, _ := outgoingToIncoming(ctx)
	token, err := TokenFromIncoming(incomingCtx)
	if err != nil {
		t.Fatalf("TokenFromIncoming: %v", err)
	}
	if token != "mi-token" {
		t.Errorf("token = %q", token)
	}
}

func TestUnaryServerInterceptorRechazaTokenInvalido(t *testing.T) {
	validate := StaticValidator([]string{"valido"})
	interceptor := UnaryServerInterceptor(validate)

	ctx := OutgoingContext(context.Background(), "invalido")
	ctx, _ = outgoingToIncoming(ctx)

	_, err := interceptor(ctx, nil, nil, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatal("se esperaba error con un token invalido")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, se esperaba Unauthenticated", status.Code(err))
	}
}

func TestUnaryServerInterceptorAceptaTokenValido(t *testing.T) {
	validate := StaticValidator([]string{"valido"})
	interceptor := UnaryServerInterceptor(validate)

	ctx := OutgoingContext(context.Background(), "valido")
	ctx, _ = outgoingToIncoming(ctx)

	resp, err := interceptor(ctx, nil, nil, func(ctx context.Context, req any) (any, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	if resp != "ok" {
		t.Errorf("resp = %v", resp)
	}
}
