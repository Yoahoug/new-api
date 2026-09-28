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
)

// A request interceptor rewrites the outbound request after the channel is
// selected and the model is mapped, but before the upstream call. It repairs
// vendor quirks — a missing session header, a body shape one provider rejects,
// a field one format forbids — that no built-in adaptor should hard-code. It
// complements the guard: a guard decides whether a request may run, an
// interceptor decides what the allowed request looks like on the wire.
const (
	// InterceptorCapability marks a host that can run meta.interceptor hooks.
	InterceptorCapability = "request-interceptor@1"

	// DefaultInterceptHook is the export an interceptor block binds to.
	DefaultInterceptHook = "intercept"

	// DefaultInterceptorTimeout bounds one hook call. Interceptors run on the
	// request path after the channel is selected, so like a guard they must
	// not stall traffic; unlike a guard they get the task-plugin budget when
	// the block asks for it, because a stream may carry a large body.
	DefaultInterceptorTimeout = 2 * time.Second
	MinInterceptorTimeout     = 100 * time.Millisecond
	MaxInterceptorTimeout     = DefaultCallTimeout

	// MaxInterceptorBodyBytes bounds the JSON body an interceptor may read and
	// rewrite. Larger requests (rare vision or audio payloads) pass unhooked:
	// a plugin that needs to see them must not silently break them either.
	MaxInterceptorBodyBytes = 4 << 20

	MaxInterceptorHeaderEntries = 64
)

// InterceptorMeta declares how one plugin joins the outbound request chain.
// The same isolation rules as the guard apply: declaring it excludes the
// task-plugin driver surface.
type InterceptorMeta struct {
	Priority  int      `json:"priority"`
	FailOpen  bool     `json:"failOpen,omitempty"`
	TimeoutMs int      `json:"timeoutMs,omitempty"`
	Methods   []string `json:"methods,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	// Models narrows the interceptor to upstream (mapped) model names, so an
	// interceptor written for one vendor endpoint follows the channel's model
	// mapping instead of duplicating it.
	Models       []string           `json:"models,omitempty"`
	Groups       []string           `json:"groups,omitempty"`
	Intercept    string             `json:"intercept,omitempty"`
	Complete     string             `json:"complete,omitempty"`
	ConfigFields []GuardConfigField `json:"configFields,omitempty"`
}

// InterceptRequest is the mutable outbound view an interceptor rewrites. Body
// is the exact JSON the host will send upstream (after conversion, param
// override, and field removal); Headers is the outbound header map the host
// has assembled so far, including channel overrides. A plugin may replace
// Body wholesale and may set, replace, or clear headers; credentials the host
// owns (Authorization and friends) are visible but read-only — attempts to
// change them are rejected.
type InterceptRequest struct {
	RequestID       string
	Method          string
	Path            string
	UpstreamBaseURL string
	UpstreamModel   string
	OriginModel     string
	Stream          bool
	RetryIndex      int
	ChannelID       int
	ChannelType     int
	UserID          int
	UsingGroup      string
	Headers         map[string]string
	Body            any
}

// JSValue renders the request with explicit key names.
func (r InterceptRequest) JSValue() map[string]any {
	headers := make(map[string]any, len(r.Headers))
	for name, value := range r.Headers {
		headers[name] = value
	}
	return map[string]any{
		"requestId":       r.RequestID,
		"method":          r.Method,
		"path":            r.Path,
		"upstreamBaseUrl": r.UpstreamBaseURL,
		"upstreamModel":   r.UpstreamModel,
		"originModel":     r.OriginModel,
		"stream":          r.Stream,
		"retryIndex":      r.RetryIndex,
		"channelId":       r.ChannelID,
		"channelType":     r.ChannelType,
		"userId":          r.UserID,
		"usingGroup":      r.UsingGroup,
		"headers":         headers,
		"body":            r.Body,
	}
}

// InterceptDecision is the hook's rewrite result.
type InterceptDecision struct {
	// Body replaces the outbound JSON when non-nil. Returning no body keeps
	// the host's bytes.
	Body any
	// Headers sets or replaces outbound headers (merged over the host map).
	Headers map[string]string
	// ClearHeaders removes outbound headers by name before Headers is merged.
	ClearHeaders []string
	// Metadata is diagnostic only and lands in the gateway log.
	Metadata map[string]string
}

// interceptorEntry is one plugin's slot in the outbound chain.
type interceptorEntry struct {
	plugin   *LoadedPlugin
	meta     InterceptorMeta
	timeout  time.Duration
	complete bool
}

func (e *interceptorEntry) claims(request InterceptRequest) bool {
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
	if len(e.meta.Models) > 0 {
		if request.UpstreamModel == "" {
			return false
		}
		folded := asciiFold(request.UpstreamModel)
		matched := false
		for _, model := range e.meta.Models {
			if asciiFold(model) == folded {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return len(e.meta.Groups) == 0 || slices.Contains(e.meta.Groups, request.UsingGroup)
}

// interceptorConfigs holds the administrator-supplied per-plugin configuration.
var interceptorConfigs atomic.Pointer[map[string]any]

// TaskPluginInterceptorConfigsKey is the options-table key holding per-plugin
// interceptor configuration, stored as a JSON object of plugin key to config.
const TaskPluginInterceptorConfigsKey = "TaskPluginInterceptorConfigs"

// SetInterceptorConfigsOption replaces the interceptor configuration snapshot.
func SetInterceptorConfigsOption(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		interceptorConfigs.Store(&map[string]any{})
		return nil
	}
	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return fmt.Errorf("interceptor configs must be a JSON object of plugin key to config: %w", err)
	}
	if parsed == nil {
		parsed = map[string]any{}
	}
	interceptorConfigs.Store(&parsed)
	return nil
}

// InterceptorConfigsSnapshot returns a copy of the stored configuration.
func InterceptorConfigsSnapshot() map[string]any {
	stored := interceptorConfigs.Load()
	if stored == nil {
		return map[string]any{}
	}
	return maps.Clone(*stored)
}

func interceptorConfigFor(pluginKey string) any {
	stored := interceptorConfigs.Load()
	if stored == nil {
		return nil
	}
	return (*stored)[pluginKey]
}

// OutboundTransform is the hook-facing result of running the outbound chain.
// Body is nil when no interceptor supplied rewritten bytes, so callers keep
// their original payload untouched.
type OutboundTransform struct {
	Body         []byte
	Headers      map[string]string
	ClearHeaders []string
	// Key names the last interceptor that contributed a change.
	Key string
	// Failed marks a fail-closed hook error. Error describes the failure and
	// the caller must abort the attempt; a broken repair must not reach the
	// vendor it was written for.
	Failed bool
	Error  error
}

// RunOutboundInterceptors feeds the outbound request through every
// interceptor that claims it, highest priority first. Each hook sees the
// previous hook's rewrite, so repairs compose. Body stays nil when no hook
// returned one, meaning "send as-is".
func (g *RoutingGeneration) RunOutboundInterceptors(ctx context.Context, request InterceptRequest) OutboundTransform {
	if g == nil || len(g.interceptors) == 0 {
		return OutboundTransform{}
	}
	transform := OutboundTransform{}
	for _, entry := range g.interceptors {
		if !entry.claims(request) {
			continue
		}
		decision, err := entry.intercept(ctx, request)
		if err != nil {
			logger.LogWarn(ctx, "plugin_interceptor event=hook_error plugin=%q hook=%q err=%q fail_open=%t",
				entry.plugin.Meta.Key, entry.meta.Intercept, err.Error(), entry.meta.FailOpen)
			if entry.meta.FailOpen {
				continue
			}
			// Fail closed: an interceptor written to repair a vendor quirk
			// must not let an unrepaired request reach the vendor it cannot
			// serve. The error propagates as a 502-shaped relay failure
			// through the caller's handler.
			return OutboundTransform{
				Failed:  true,
				Key:     entry.plugin.Meta.Key,
				Headers: transform.Headers,
				Error:   fmt.Errorf("request interceptor %s failed: %w", entry.plugin.Meta.Name, err),
			}
		}
		if decision.Body != nil {
			if encoded, encodeErr := common.Marshal(decision.Body); encodeErr == nil {
				request.Body = decision.Body
				transform.Key = entry.plugin.Meta.Key
				transform.Body = encoded
			} else {
				return OutboundTransform{
					Failed: true,
					Key:    entry.plugin.Meta.Key,
					Error:  fmt.Errorf("request interceptor %s returned a body that cannot be encoded: %w", entry.plugin.Meta.Name, encodeErr),
				}
			}
		}
		if len(decision.Headers) > 0 || len(decision.ClearHeaders) > 0 {
			merged := make(map[string]string, len(transform.Headers)+len(decision.Headers))
			maps.Copy(merged, transform.Headers)
			for _, name := range decision.ClearHeaders {
				delete(merged, strings.ToLower(strings.TrimSpace(name)))
			}
			maps.Copy(merged, decision.Headers)
			transform.Headers = merged
		}
		if len(decision.Metadata) > 0 {
			logger.LogDebug(ctx, "plugin_interceptor event=metadata plugin=%q detail=%v",
				entry.plugin.Meta.Key, decision.Metadata)
		}
		if decision.Body != nil {
			if encoded, encodeErr := common.Marshal(decision.Body); encodeErr == nil {
				transform.Body = encoded
			} else {
				return OutboundTransform{
					Failed: true,
					Key:    entry.plugin.Meta.Key,
					Error:  fmt.Errorf("request interceptor %s returned a body that cannot be encoded: %w", entry.plugin.Meta.Name, encodeErr),
				}
			}
		}
	}
	return transform
}

func (e *interceptorEntry) intercept(ctx context.Context, request InterceptRequest) (InterceptDecision, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	value := request.JSValue()
	value["config"] = interceptorConfigFor(e.plugin.Meta.Key)
	raw, err := e.plugin.Engine.Call(timeoutCtx, e.meta.Intercept, value)
	if err != nil {
		return InterceptDecision{}, err
	}
	return decodeInterceptDecision(raw)
}

// NotifyInterceptorCompletion delivers one terminal event to every claiming
// interceptor with a complete hook, detached from the request context.
func (g *RoutingGeneration) NotifyInterceptorCompletion(request InterceptRequest, completion GuardCompletion) {
	if g == nil || len(g.interceptors) == 0 {
		return
	}
	notifying := make([]*interceptorEntry, 0, len(g.interceptors))
	for _, entry := range g.interceptors {
		if entry.complete && entry.claims(request) {
			notifying = append(notifying, entry)
		}
	}
	if len(notifying) == 0 {
		return
	}
	detached := context.WithoutCancel(context.Background())
	value := completion.jsValue()
	for _, entry := range notifying {
		go func() {
			timeoutCtx, cancel := context.WithTimeout(detached, entry.timeout)
			defer cancel()
			if _, err := entry.plugin.Engine.Call(timeoutCtx, entry.meta.Complete, value); err != nil {
				logger.LogWarn(detached, "plugin_interceptor event=complete_error plugin=%q hook=%q err=%q",
					entry.plugin.Meta.Key, entry.meta.Complete, err.Error())
			}
		}()
	}
}

// HasOutboundInterceptors reports whether the generation holds any interceptor.
func (g *RoutingGeneration) HasOutboundInterceptors() bool {
	return g != nil && len(g.interceptors) > 0
}

// OutboundInterceptors returns the installed interceptors in chain order.
func (g *RoutingGeneration) OutboundInterceptors() []*LoadedPlugin {
	if g == nil || len(g.interceptors) == 0 {
		return nil
	}
	interceptors := make([]*LoadedPlugin, 0, len(g.interceptors))
	for _, entry := range g.interceptors {
		interceptors = append(interceptors, entry.plugin)
	}
	return interceptors
}

// IsRequestInterceptor reports whether the plugin rewrites outbound requests.
func (m Meta) IsRequestInterceptor() bool {
	return m.Interceptor != nil
}

// interceptorHeaderPolicy guards the fields the host owns on the outbound
// request. An interceptor may add vendor headers, session ids, and routing
// hints, but it must not be able to re-aim authentication.
func interceptorHeaderPolicy(name string) error {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return fmt.Errorf("header name must not be empty")
	}
	if normalized == "host" {
		return fmt.Errorf("header %q is owned by the channel and cannot be changed by an interceptor", name)
	}
	if slices.Contains([]string{"authorization", "proxy-authorization", "cookie", "content-length", "transfer-encoding", "content-encoding", "upgrade", "connection"}, normalized) {
		return fmt.Errorf("header %q is owned by the host and cannot be changed by an interceptor", name)
	}
	if strings.HasSuffix(normalized, "-secret") || strings.HasSuffix(normalized, "-password") {
		return fmt.Errorf("header %q is owned by the host and cannot be changed by an interceptor", name)
	}
	return nil
}

func decodeInterceptDecision(value any) (InterceptDecision, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return InterceptDecision{}, fmt.Errorf("hook must return an object")
	}
	decision := InterceptDecision{}
	if rawBody, exists := object["body"]; exists && rawBody != nil {
		if _, isNull := rawBody.(map[string]any); !isNull {
			// Anything other than an object body is a contract breach: the
			// outbound request is a JSON document.
			switch rawBody.(type) {
			case map[string]any, nil:
			default:
				return InterceptDecision{}, fmt.Errorf("interceptor body must be a JSON object")
			}
		}
		decision.Body = rawBody
	}
	if headers, exists := object["headers"].(map[string]any); exists && headers != nil {
		decision.Headers = make(map[string]string, len(headers))
		for name, headerValue := range headers {
			text, isString := headerValue.(string)
			if !isString {
				return InterceptDecision{}, fmt.Errorf("header %q must be a string", name)
			}
			if err := interceptorHeaderPolicy(name); err != nil {
				return InterceptDecision{}, err
			}
			decision.Headers[strings.TrimSpace(name)] = text
		}
	}
	if clear, exists := object["clearHeaders"].([]any); exists {
		decision.ClearHeaders = make([]string, 0, len(clear))
		for _, name := range clear {
			text, isString := name.(string)
			if !isString {
				return InterceptDecision{}, fmt.Errorf("clearHeaders entries must be strings")
			}
			if err := interceptorHeaderPolicy(text); err != nil {
				return InterceptDecision{}, err
			}
			decision.ClearHeaders = append(decision.ClearHeaders, strings.TrimSpace(text))
		}
	}
	if metadata, exists := object["metadata"].(map[string]any); exists && metadata != nil {
		decision.Metadata = make(map[string]string, len(metadata))
		for name, entry := range metadata {
			text, isString := entry.(string)
			if !isString {
				return InterceptDecision{}, fmt.Errorf("metadata %q must be a string", name)
			}
			decision.Metadata[name] = text
		}
	}
	return decision, nil
}

func interceptorTimeout(meta InterceptorMeta) time.Duration {
	if meta.TimeoutMs <= 0 {
		return DefaultInterceptorTimeout
	}
	return min(max(time.Duration(meta.TimeoutMs)*time.Millisecond, MinInterceptorTimeout), MaxInterceptorTimeout)
}

// normalizeInterceptorPluginMeta finishes validating a manifest that declares
// meta.interceptor. Unlike a guard, an interceptor deliberately touches the
// outbound request, so it may not also be a guard: one plugin, one question.
func normalizeInterceptorPluginMeta(meta *Meta) error {
	if err := normalizeInterceptorMeta(meta.Interceptor); err != nil {
		return err
	}
	if !slices.Contains(meta.RequiredCapabilities, InterceptorCapability) {
		return fmt.Errorf("plugin %s declares meta.interceptor and must require the %s capability", meta.Key, InterceptorCapability)
	}
	return rejectTaskPluginSurface(meta, "meta.interceptor")
}

func normalizeInterceptorMeta(meta *InterceptorMeta) error {
	meta.Intercept = strings.TrimSpace(meta.Intercept)
	if meta.Intercept == "" {
		meta.Intercept = DefaultInterceptHook
	}
	meta.Complete = strings.TrimSpace(meta.Complete)
	if meta.Priority < math.MinInt32 || meta.Priority > math.MaxInt32 {
		return fmt.Errorf("plugin meta interceptor priority must be a signed 32-bit integer")
	}
	if meta.TimeoutMs < 0 {
		return fmt.Errorf("plugin meta interceptor timeoutMs must be positive")
	}
	for index, method := range meta.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, method) {
			return fmt.Errorf("plugin meta interceptor methods must be HTTP methods")
		}
		meta.Methods[index] = method
	}
	for _, list := range [][]string{meta.Methods, meta.Paths, meta.Models, meta.Groups} {
		if len(list) > MaxGuardScopeEntries {
			return fmt.Errorf("plugin meta interceptor scope lists must not exceed %d entries", MaxGuardScopeEntries)
		}
	}
	seenPaths := make(map[string]struct{}, len(meta.Paths))
	for _, path := range meta.Paths {
		if err := validateGuardPath(path); err != nil {
			return err
		}
		if _, duplicate := seenPaths[path]; duplicate {
			return fmt.Errorf("plugin meta interceptor paths must be unique")
		}
		seenPaths[path] = struct{}{}
	}
	for _, model := range meta.Models {
		if model == "" || strings.TrimSpace(model) != model {
			return fmt.Errorf("plugin meta interceptor models must contain non-empty canonical names")
		}
	}
	for _, group := range meta.Groups {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("plugin meta interceptor groups must contain non-empty names")
		}
	}
	if len(meta.ConfigFields) > MaxGuardConfigFields {
		return fmt.Errorf("plugin meta interceptor configFields must not exceed %d entries", MaxGuardConfigFields)
	}
	seenFields := make(map[string]struct{}, len(meta.ConfigFields))
	for index := range meta.ConfigFields {
		field := &meta.ConfigFields[index]
		field.Name = strings.TrimSpace(field.Name)
		if field.Name == "" || len(field.Name) > MaxGuardConfigFieldName {
			return fmt.Errorf("plugin meta interceptor configFields names must be 1 to %d characters", MaxGuardConfigFieldName)
		}
		if _, duplicate := seenFields[field.Name]; duplicate {
			return fmt.Errorf("plugin meta interceptor configFields must be unique")
		}
		seenFields[field.Name] = struct{}{}
		if !slices.Contains(guardConfigFieldTypes, field.Type) {
			return fmt.Errorf("plugin meta interceptor configFields[%d] type must be one of %s", index, strings.Join(guardConfigFieldTypes, ", "))
		}
	}
	return nil
}

// decodeInterceptorMeta reads the manifest's interceptor block with the same
// unknown-field strictness as the guard block.
func decodeInterceptorMeta(value any) (*InterceptorMeta, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("plugin meta interceptor must be an object")
	}
	for field := range object {
		switch field {
		case "priority", "failOpen", "timeoutMs", "methods", "paths", "models", "groups", "intercept", "complete", "configFields":
		default:
			return nil, fmt.Errorf("plugin meta interceptor has unknown field %q", field)
		}
	}
	meta := &InterceptorMeta{}
	var err error
	if meta.Priority, err = integerMetaField(object, "priority"); err != nil {
		return nil, err
	}
	if meta.TimeoutMs, err = integerMetaField(object, "timeoutMs"); err != nil {
		return nil, err
	}
	if meta.FailOpen, err = booleanMetaField(object, "failOpen"); err != nil {
		return nil, err
	}
	if meta.Intercept, err = stringMetaField(object, "intercept"); err != nil {
		return nil, err
	}
	if meta.Complete, err = stringMetaField(object, "complete"); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		name   string
		target *[]string
	}{
		{"methods", &meta.Methods},
		{"paths", &meta.Paths},
		{"models", &meta.Models},
		{"groups", &meta.Groups},
	} {
		values, sliceErr := strictStringSlice(object, field.name)
		if sliceErr != nil {
			return nil, sliceErr
		}
		*field.target = values
	}
	if rawFields, exists := object["configFields"]; exists {
		fields, fieldsErr := decodeGuardConfigFields(rawFields)
		if fieldsErr != nil {
			return nil, fmt.Errorf("plugin meta interceptor configFields: %w", fieldsErr)
		}
		meta.ConfigFields = fields
	}
	return meta, nil
}

// verifyInterceptorExports checks that every hook the block binds to exists.
func verifyInterceptorExports(engine *Engine, meta Meta) error {
	for _, hook := range []struct {
		bind string
		kind string
	}{
		{meta.Interceptor.Intercept, "decision hook"},
		{meta.Interceptor.Complete, "completion hook"},
	} {
		if hook.bind == "" {
			continue
		}
		callable, err := engine.HasCallablePath(context.Background(), hook.bind)
		if err != nil {
			return err
		}
		if !callable {
			return fmt.Errorf("plugin %s declares meta.interceptor %s %q but does not export it", meta.Key, hook.kind, hook.bind)
		}
	}
	return nil
}
