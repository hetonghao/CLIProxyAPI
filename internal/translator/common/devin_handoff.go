package common

import "context"

type devinHandoffsKey struct{}

// WithDevinPlaintextHandoffs binds the negotiated wire tool names to one request.
func WithDevinPlaintextHandoffs(ctx context.Context, tools map[string]bool) context.Context {
	if len(tools) == 0 {
		return ctx
	}
	return context.WithValue(ctx, devinHandoffsKey{}, tools)
}

func DevinPlaintextHandoffs(ctx context.Context) map[string]bool {
	if ctx == nil {
		return nil
	}
	tools, _ := ctx.Value(devinHandoffsKey{}).(map[string]bool)
	return tools
}
