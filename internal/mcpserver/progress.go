package mcpserver

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// progressToken extracts the MCP progress token from a tool call request, if
// the caller supplied one via _meta.progressToken. Returns nil when absent.
func progressToken(req mcp.CallToolRequest) any {
	if req.Params.Meta == nil {
		return nil
	}
	return req.Params.Meta.ProgressToken
}

// newProgressEmitter returns a throttled progress emitter. It forwards to
// send only when the integer percent complete changes (or on completion),
// and drops any non-monotonic (regressing) current values.
func newProgressEmitter(send func(progress, total int, msg string)) func(current, total int, msg string) {
	lastCurrent := -1
	lastPercent := -1

	return func(current, total int, msg string) {
		if total <= 0 {
			return
		}
		if current < lastCurrent {
			return
		}

		percent := current * 100 / total
		if percent == lastPercent && current != total {
			return
		}

		lastCurrent = current
		lastPercent = percent
		// Completion always emits, bypassing the percent throttle. Callers whose
		// progress source signals 100% exactly once (espflasher's FlashImages loop
		// and tickErase both do) get one completion tick; a source that reports
		// current==total repeatedly would emit a duplicate notification here
		// (MCP-tolerant, just wasteful).
		send(current, total, msg)
	}
}

// sendProgress returns the production progress sender for a given request
// context and progress token. If token is nil (the caller didn't ask for
// progress notifications), it returns a no-op. Send errors are swallowed:
// transient conditions (blocked/unintialized notification channel) must
// never fail the underlying tool call.
func sendProgress(ctx context.Context, token any) func(progress, total int, msg string) {
	if token == nil {
		return func(progress, total int, msg string) {}
	}

	return func(progress, total int, msg string) {
		srv := server.ServerFromContext(ctx)
		if srv == nil {
			return
		}
		_ = srv.SendNotificationToClient(ctx, "notifications/progress", map[string]any{
			"progressToken": token,
			"progress":      progress,
			"total":         total,
			"message":       msg,
		})
	}
}
