package jsplugin

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pluginkit"
)

// A task plugin drives one upstream model. A request guard decides whether a
// request is allowed to reach an upstream model at all. The two plugin kinds
// share the Sobek sandbox, the registry, the generation publication and the
// admin surfaces, but they answer different questions and therefore have
// separate hooks: a guard never builds a request, never parses a response, and
// never reports usage.

// Terminal outcomes reported to the optional completion hook. The host
// classifies the request so a plugin never has to infer the outcome.
const (
	GuardOutcomeDenied   = "denied"
	GuardOutcomeSucceed  = "succeeded"
	GuardOutcomeFailed   = "failed"
	GuardOutcomeCanceled = "canceled"
)

// GuardRequest is the immutable view a guard decides from. The host builds it
// after authentication and before channel selection, so a guard can neither
// observe nor influence an upstream credential, a channel, or a price.
type GuardRequest struct {
	RequestID  string
	UnixNow    int64
	Method     string
	Path       string
	ClientIP   string
	UserID     int
	TokenID    int
	TokenName  string
	UserGroup  string
	UsingGroup string
	Model      string
	Stream     bool
	Headers    map[string]string
	Body       any
}

// Group is the group the request is routed and billed under. A token group
// override wins over the account group so a guard sees what the relay will use.
func (r GuardRequest) Group() string {
	if r.UsingGroup != "" {
		return r.UsingGroup
	}
	return r.UserGroup
}

// JSValue renders the request as an explicit key map, the way the rest of the
// plugin surface does, so JavaScript property names never depend on Go field
// names or struct tags.
func (r GuardRequest) JSValue() map[string]any {
	headers := make(map[string]any, len(r.Headers))
	for name, value := range r.Headers {
		headers[name] = value
	}
	return map[string]any{
		"requestId":  r.RequestID,
		"unixNow":    r.UnixNow,
		"method":     r.Method,
		"path":       r.Path,
		"clientIp":   r.ClientIP,
		"userId":     r.UserID,
		"tokenId":    r.TokenID,
		"tokenName":  r.TokenName,
		"userGroup":  r.UserGroup,
		"usingGroup": r.UsingGroup,
		"model":      r.Model,
		"stream":     r.Stream,
		"headers":    headers,
		"body":       r.Body,
	}
}

// GuardDecision is what a guard hook returns. Allowing is the default posture,
// so a hook has to state a denial explicitly.
type GuardDecision struct {
	Allow    bool
	Status   int
	Code     string
	Message  string
	Headers  map[string]string
	Metadata map[string]string
}

// GuardCompletion is the single terminal event one request produces. The host
// sends it once, asynchronously, so a client that disconnects still notifies.
type GuardCompletion struct {
	RequestID   string
	Outcome     string
	StatusCode  int
	GuardKey    string
	StartedAt   time.Time
	CompletedAt time.Time
}

func (e GuardCompletion) jsValue() map[string]any {
	return map[string]any{
		"requestId":   e.RequestID,
		"outcome":     e.Outcome,
		"statusCode":  e.StatusCode,
		"guardKey":    e.GuardKey,
		"startedAt":   e.StartedAt.Unix(),
		"completedAt": e.CompletedAt.Unix(),
	}
}

// GuardOutcome is the host-side verdict of running the whole chain.
type GuardOutcome struct {
	Allowed bool
	// Key names the guard that produced the verdict. It is empty when no guard
	// claimed the request.
	Key string
	// Failed marks a hook error rather than a decision, so a caller can report
	// the difference and apply the declaring guard's fail-open setting.
	Failed   bool
	Status   int
	Code     string
	Message  string
	Headers  map[string]string
	Metadata map[string]string
}

// guardEntry is one plugin's slot in the chain, built with the generation so
// ordering is fixed for the lifetime of that generation.
type guardEntry struct {
	plugin   *LoadedPlugin
	meta     pluginkit.GuardMeta
	timeout  time.Duration
	complete bool
}

func (e *guardEntry) claims(request GuardRequest) bool {
	if len(e.meta.Methods) > 0 && !slices.Contains(e.meta.Methods, strings.ToUpper(request.Method)) {
		return false
	}
	if len(e.meta.Paths) > 0 {
		matched := false
		for _, prefix := range e.meta.Paths {
			if strings.HasPrefix(request.Path, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, prefix := range e.meta.ExcludePaths {
		if strings.HasPrefix(request.Path, prefix) {
			return false
		}
	}
	if len(e.meta.Models) > 0 {
		if request.Model == "" {
			return false
		}
		folded := ASCIIFold(request.Model)
		matched := false
		for _, model := range e.meta.Models {
			if ASCIIFold(model) == folded {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return len(e.meta.Groups) == 0 || slices.Contains(e.meta.Groups, request.Group())
}

// TaskPluginGuardConfigsKey is the options-table key holding the
// administrator-supplied configuration of every installed request guard, as a
// JSON object of plugin key to plugin-defined config. It rides the existing
// generic options storage so no new table or migration is needed and the value
// is editable and auditable through the same surface as every other option.
const TaskPluginGuardConfigsKey = "TaskPluginGuardConfigs"

// guardConfigs holds the administrator-supplied per-plugin configuration. It
// lives outside the routing generation so a rule can change without
// recompiling or re-publishing plugins, and is read without a lock on the
// request path.
var guardConfigs atomic.Pointer[map[string]any]

// SetGuardConfigsOption replaces the guard configuration snapshot from its
// stored JSON object. Invalid input is reported and leaves the previous
// snapshot in place, so a malformed option cannot silently strip every rule.
func SetGuardConfigsOption(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		guardConfigs.Store(&map[string]any{})
		return nil
	}
	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return fmt.Errorf("guard configs must be a JSON object of plugin key to config: %w", err)
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	guardConfigs.Store(&parsed)
	return nil
}

// GuardConfigsSnapshot returns a copy of the stored configuration so an
// administrator can read back what is actually in effect.
func GuardConfigsSnapshot() map[string]any {
	stored := guardConfigs.Load()
	if stored == nil {
		return map[string]any{}
	}
	return maps.Clone(*stored)
}

func guardConfigFor(pluginKey string) any {
	stored := guardConfigs.Load()
	if stored == nil {
		return nil
	}
	return (*stored)[pluginKey]
}

// HasRequestGuards reports whether the generation holds any request guard, so
// a caller can skip building a guard request when none is installed.
func (g *RoutingGeneration) HasRequestGuards() bool {
	return g != nil && len(g.guards) > 0
}

// RunRequestGuards evaluates every guard that claims the request, highest
// priority first, and stops at the first denial. An exclusive guard that claims
// the request is the sole authority for it: its allow ends the chain, so no
// other guard can loosen a decision the exclusive guard owns.
func (g *RoutingGeneration) RunRequestGuards(ctx context.Context, request GuardRequest) GuardOutcome {
	if g == nil || len(g.guards) == 0 {
		return GuardOutcome{Allowed: true}
	}
	for _, entry := range g.guards {
		if !entry.claims(request) {
			continue
		}
		decision, err := entry.decide(ctx, request)
		if err != nil {
			logger.LogWarn(ctx, "plugin_guard event=hook_error plugin=%q hook=%q err=%q fail_open=%t",
				entry.plugin.Meta.Key, entry.meta.Authorize, err.Error(), entry.meta.FailOpen)
			if entry.meta.FailOpen {
				continue
			}
			return GuardOutcome{
				Key:      entry.plugin.Meta.Key,
				Failed:   true,
				Status:   403,
				Code:     "permission_guard_error",
				Message:  fmt.Sprintf("request guard %s failed; the request was denied", entry.plugin.Meta.Name),
				Metadata: map[string]string{"guard_error": err.Error()},
			}
		}
		if !decision.Allow {
			return GuardOutcome{
				Key:      entry.plugin.Meta.Key,
				Status:   decision.status(),
				Code:     decision.code(),
				Message:  decision.message(entry.plugin.Meta.Name),
				Headers:  decision.Headers,
				Metadata: decision.Metadata,
			}
		}
		if entry.meta.Exclusive {
			return GuardOutcome{Allowed: true, Key: entry.plugin.Meta.Key}
		}
	}
	return GuardOutcome{Allowed: true}
}

func (e *guardEntry) decide(ctx context.Context, request GuardRequest) (GuardDecision, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	value := request.JSValue()
	value["config"] = guardConfigFor(e.plugin.Meta.Key)
	raw, err := e.plugin.Engine.Call(timeoutCtx, e.meta.Authorize, value)
	if err != nil {
		return GuardDecision{}, err
	}
	decision, err := decodeGuardDecision(raw)
	if err != nil {
		return GuardDecision{}, fmt.Errorf("guard hook %q returned an invalid decision: %w", e.meta.Authorize, err)
	}
	return decision, nil
}

// RequestGuards returns the installed request guards in chain order, highest
// priority first, so an administration client can present them in the order
// their decisions are actually made.
func (g *RoutingGeneration) RequestGuards() []*LoadedPlugin {
	if g == nil || len(g.guards) == 0 {
		return nil
	}
	guards := make([]*LoadedPlugin, 0, len(g.guards))
	for _, entry := range g.guards {
		guards = append(guards, entry.plugin)
	}
	return guards
}

// NotifyGuardCompletion delivers one terminal event to every guard that was
// consulted for the request. It never blocks the response: a guard that cannot
// keep up must not delay a client.
func (g *RoutingGeneration) NotifyGuardCompletion(request GuardRequest, completion GuardCompletion) {
	if g == nil || len(g.guards) == 0 {
		return
	}
	notifying := make([]*guardEntry, 0, len(g.guards))
	for _, entry := range g.guards {
		if entry.complete && entry.claims(request) {
			notifying = append(notifying, entry)
		}
	}
	if len(notifying) == 0 {
		return
	}
	// Detach from the request context: a client that disconnects mid-stream
	// still has to release whatever the guard was holding.
	detached := context.WithoutCancel(context.Background())
	value := completion.jsValue()
	for _, entry := range notifying {
		go func() {
			timeoutCtx, cancel := context.WithTimeout(detached, entry.timeout)
			defer cancel()
			if _, err := entry.plugin.Engine.Call(timeoutCtx, entry.meta.Complete, value); err != nil {
				logger.LogWarn(detached, "plugin_guard event=complete_error plugin=%q hook=%q err=%q",
					entry.plugin.Meta.Key, entry.meta.Complete, err.Error())
			}
		}()
	}
}

func (d GuardDecision) status() int {
	// 400 is the lowest status that may carry an authorization refusal, and it
	// keeps a mistaken 200 or 3xx from being served as a decision.
	if d.Status < 400 || d.Status > 599 {
		return 403
	}
	return d.Status
}

func (d GuardDecision) code() string {
	if code := strings.TrimSpace(d.Code); code != "" {
		return code
	}
	return "permission_denied"
}

func (d GuardDecision) message(pluginName string) string {
	if message := strings.TrimSpace(d.Message); message != "" {
		return message
	}
	return fmt.Sprintf("request denied by %s", pluginName)
}

func decodeGuardDecision(value any) (GuardDecision, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return GuardDecision{}, fmt.Errorf("hook must return an object with a boolean allow field")
	}
	allow, ok := object["allow"].(bool)
	if !ok {
		return GuardDecision{}, fmt.Errorf("hook must return a boolean allow field")
	}
	decision := GuardDecision{Allow: allow}
	if status, isNumber := jsonNumber(object["status"]); isNumber {
		decision.Status = int(status)
	}
	decision.Code = trimmedOptionalString(object, "code")
	decision.Message = trimmedOptionalString(object, "message")
	headers, hasHeaders := object["headers"].(map[string]any)
	if hasHeaders && headers != nil {
		decision.Headers = make(map[string]string, len(headers))
		for name, headerValue := range headers {
			text, isString := headerValue.(string)
			if !isString {
				return GuardDecision{}, fmt.Errorf("response header %q must be a string", name)
			}
			if !guardResponseHeaderAllowed(name) {
				return GuardDecision{}, fmt.Errorf("response header %q cannot be set by a request guard", name)
			}
			decision.Headers[name] = text
		}
	}
	if metadata, hasMetadata := object["metadata"].(map[string]any); hasMetadata && metadata != nil {
		decision.Metadata = make(map[string]string, len(metadata))
		for name, entry := range metadata {
			text, isString := entry.(string)
			if !isString {
				return GuardDecision{}, fmt.Errorf("metadata %q must be a string", name)
			}
			decision.Metadata[name] = text
		}
	}
	return decision, nil
}

// guardResponseHeaderBlocked lists the response headers a guard may not set.
// A denial is still a real HTTP response: framing, routing, and content
// negotiation headers stay under host control so a plugin cannot rewrite the
// shape of the error the client receives.
var guardResponseHeaderBlocked = []string{
	"content-length", "content-type", "transfer-encoding", "connection",
	"content-encoding", "upgrade", "location",
}

func guardResponseHeaderAllowed(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return false
	}
	return !slices.Contains(guardResponseHeaderBlocked, normalized)
}

func trimmedOptionalString(object map[string]any, key string) string {
	text, _ := object[key].(string)
	return strings.TrimSpace(text)
}

// jsonNumber reads a JSON number regardless of which decoder produced it. A
// JavaScript literal exports as int64 when it is integral and float64 when it
// is not, while host JSON decoding always produces float64, so a guard's
// `status` and a management client's `integer` field both land here.
func jsonNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int64:
		return float64(number), true
	case int:
		return float64(number), true
	default:
		return 0, false
	}
}

// IsGuardConfigValue reports whether a stored value satisfies a declared
// config field. It is the single check behind the administration API and the
// dry-run surface, so an administrator and the host agree on what a field
// accepts.
func IsGuardConfigValue(field GuardConfigField, value any) bool {
	switch field.Type {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := jsonNumber(value)
		return ok
	case "integer":
		number, ok := jsonNumber(value)
		return ok && number == math.Trunc(number)
	case "enum":
		text, ok := value.(string)
		return ok && slices.Contains(field.EnumValues, text)
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	default:
		return false
	}
}
