package pluginkit

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// The request guard and outbound interceptor are custom plugin kinds of this
// fork, declared by meta.guard and meta.interceptor. They share the manifest
// contract with the task plugin, so their metadata lives here beside Meta;
// the host-side execution (chain ordering, hook calls, HTTP surfaces) stays
// in pkg/jsplugin.

const (
	// GuardCapability marks a host that can run meta.guard hooks. A guard
	// plugin requires it so an older host rejects the plugin instead of
	// loading a manifest whose permission hooks it would silently ignore.
	GuardCapability = "request-guard@1"

	// InterceptorCapability marks a host that can run meta.interceptor hooks.
	InterceptorCapability = "request-interceptor@1"

	// DefaultGuardHook is the export a guard block binds its decision to.
	DefaultGuardHook = "authorize"
	// DefaultGuardCompleteHook is the optional terminal-notification export.
	DefaultGuardCompleteHook = "onComplete"

	// DefaultGuardTimeout is deliberately shorter than DefaultCallTimeout: a
	// guard gates every request, so a slow guard costs latency on all traffic.
	DefaultGuardTimeout = 2 * time.Second
	MinGuardTimeout     = 100 * time.Millisecond
	MaxGuardTimeout     = DefaultCallTimeout

	MaxGuardConfigFields    = 64
	MaxGuardConfigFieldName = 64
	MaxGuardScopeEntries    = 128
	MaxGuardPathLength      = 191

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

var guardConfigFieldTypes = []string{"string", "number", "integer", "boolean", "enum", "array", "object"}

// GuardMeta declares how one plugin participates in the request guard chain. A
// plugin that declares it is a guard; a plugin that does not is a task plugin.
// The two never mix: a guard needs no models, routes, or usage schema.
type GuardMeta struct {
	Priority     int                `json:"priority"`
	Exclusive    bool               `json:"exclusive,omitempty"`
	FailOpen     bool               `json:"failOpen,omitempty"`
	TimeoutMs    int                `json:"timeoutMs,omitempty"`
	Methods      []string           `json:"methods,omitempty"`
	Paths        []string           `json:"paths,omitempty"`
	ExcludePaths []string           `json:"excludePaths,omitempty"`
	Models       []string           `json:"models,omitempty"`
	Groups       []string           `json:"groups,omitempty"`
	Authorize    string             `json:"authorize,omitempty"`
	Complete     string             `json:"complete,omitempty"`
	ConfigFields []GuardConfigField `json:"configFields,omitempty"`
}

// GuardConfigField describes one administrator-supplied setting so a
// management client can render a form without knowing the plugin's rules.
type GuardConfigField struct {
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	EnumValues  []string      `json:"enumValues,omitempty"`
	Description LocalizedText `json:"description,omitempty"`
}

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

// IsRequestGuard reports whether the plugin guards requests instead of
// driving tasks or serving provider routes.
func (m Meta) IsRequestGuard() bool {
	return m.Guard != nil
}

// IsRequestInterceptor reports whether the plugin rewrites outbound requests
// instead of driving tasks or serving provider routes.
func (m Meta) IsRequestInterceptor() bool {
	return m.Interceptor != nil
}

// TaskPluginBindableError explains why a plugin cannot back a channel. A
// request guard answers whether a request may run and has no upstream task of
// its own, so it is never bindable to a channel.
func (m Meta) TaskPluginBindableError() error {
	if m.Guard != nil {
		return fmt.Errorf("plugin %s is a request guard and cannot be bound to a channel", m.Key)
	}
	return nil
}

// decodeGuardMeta reads the manifest's guard block. An unknown field is an
// error, so a typo cannot silently narrow a guard's scope.
func decodeGuardMeta(value any) (*GuardMeta, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("plugin meta guard must be an object")
	}
	for field := range object {
		switch field {
		case "priority", "exclusive", "failOpen", "timeoutMs", "methods", "paths", "excludePaths", "models", "groups", "authorize", "complete", "configFields":
		default:
			return nil, fmt.Errorf("plugin meta guard has unknown field %q", field)
		}
	}
	guard := &GuardMeta{}
	var err error
	if guard.Priority, err = integerMetaField(object, "priority"); err != nil {
		return nil, err
	}
	if guard.TimeoutMs, err = integerMetaField(object, "timeoutMs"); err != nil {
		return nil, err
	}
	if guard.Exclusive, err = guardBooleanMetaField(object, "exclusive"); err != nil {
		return nil, err
	}
	if guard.FailOpen, err = guardBooleanMetaField(object, "failOpen"); err != nil {
		return nil, err
	}
	if guard.Authorize, err = stringMetaField(object, "authorize"); err != nil {
		return nil, err
	}
	if guard.Complete, err = stringMetaField(object, "complete"); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		name   string
		target *[]string
	}{
		{"methods", &guard.Methods},
		{"paths", &guard.Paths},
		{"excludePaths", &guard.ExcludePaths},
		{"models", &guard.Models},
		{"groups", &guard.Groups},
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
			return nil, fmt.Errorf("plugin meta guard configFields: %w", fieldsErr)
		}
		guard.ConfigFields = fields
	}
	return guard, nil
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
	if meta.FailOpen, err = guardBooleanMetaField(object, "failOpen"); err != nil {
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

// decodeGuardConfigFields reads a configFields array shared by the guard and
// interceptor blocks.
func decodeGuardConfigFields(value any) ([]GuardConfigField, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array")
	}
	fields := make([]GuardConfigField, 0, len(items))
	for _, item := range items {
		fieldObject, isObject := item.(map[string]any)
		if !isObject {
			return nil, fmt.Errorf("entries must be objects")
		}
		for name := range fieldObject {
			if name != "name" && name != "type" && name != "enumValues" && name != "description" {
				return nil, fmt.Errorf("entry has unknown field %q", name)
			}
		}
		field := GuardConfigField{}
		var fieldErr error
		if field.Name, fieldErr = stringMetaField(fieldObject, "name"); fieldErr != nil {
			return nil, fieldErr
		}
		if field.Type, fieldErr = stringMetaField(fieldObject, "type"); fieldErr != nil {
			return nil, fieldErr
		}
		if field.Description, fieldErr = localizedTextMetaField(fieldObject, "description", maxUsageFieldDescriptionRunes); fieldErr != nil {
			return nil, fieldErr
		}
		if field.EnumValues, fieldErr = strictStringSlice(fieldObject, "enumValues"); fieldErr != nil {
			return nil, fieldErr
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func guardBooleanMetaField(object map[string]any, name string) (bool, error) {
	raw, exists := object[name]
	if !exists {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("plugin meta guard field %q must be a boolean", name)
	}
	return value, nil
}

// normalizeGuardPluginMeta finishes validating a manifest that declares
// meta.guard. It runs after the checks every plugin shares (identity, version,
// localized copy) and returns before the task-plugin checks, because a guard
// drives no upstream task: it has no models, no routes, no billing, and no
// outbound request of its own.
func normalizeGuardPluginMeta(meta *Meta) error {
	if meta.Interceptor != nil {
		return fmt.Errorf("plugin %s cannot declare both guard and interceptor", meta.Key)
	}
	if err := normalizeGuardMeta(meta.Guard); err != nil {
		return err
	}
	if !slices.Contains(meta.RequiredCapabilities, GuardCapability) {
		return fmt.Errorf("plugin %s declares meta.guard and must require the %s capability", meta.Key, GuardCapability)
	}
	return rejectTaskPluginSurface(meta, "meta.guard")
}

// normalizeInterceptorPluginMeta finishes validating a manifest that declares
// meta.interceptor.
func normalizeInterceptorPluginMeta(meta *Meta) error {
	if err := normalizeInterceptorMeta(meta.Interceptor); err != nil {
		return err
	}
	if !slices.Contains(meta.RequiredCapabilities, InterceptorCapability) {
		return fmt.Errorf("plugin %s declares meta.interceptor and must require the %s capability", meta.Key, InterceptorCapability)
	}
	return rejectTaskPluginSurface(meta, "meta.interceptor")
}

// rejectTaskPluginSurface enforces the one-kind-per-plugin rule shared by every
// non-task plugin kind. The capability label names the declaring block so the
// upload error says which surface conflicts with the task-plugin fields.
func rejectTaskPluginSurface(meta *Meta, capabilityLabel string) error {
	if meta.FetchMode != "" {
		return fmt.Errorf("plugin %s declares %s and must not declare fetchMode", meta.Key, capabilityLabel)
	}
	if len(meta.Models) > 0 {
		return fmt.Errorf("plugin %s declares %s and must not declare models", meta.Key, capabilityLabel)
	}
	if len(meta.Routes) > 0 || len(meta.Protocols) > 0 {
		return fmt.Errorf("plugin %s declares %s and must not declare routes or protocols", meta.Key, capabilityLabel)
	}
	if len(meta.UsageSchema) > 0 || len(meta.UsageProfiles) > 0 || len(meta.UsageExamples) > 0 {
		return fmt.Errorf("plugin %s declares %s and must not declare a usage schema", meta.Key, capabilityLabel)
	}
	if len(meta.ChannelTypes) > 0 {
		return fmt.Errorf("plugin %s declares %s and must not declare channelTypes", meta.Key, capabilityLabel)
	}
	if len(meta.Upstreams) > 0 {
		return fmt.Errorf("plugin %s declares %s and must not declare upstreams", meta.Key, capabilityLabel)
	}
	return nil
}

func normalizeGuardMeta(guard *GuardMeta) error {
	guard.Authorize = strings.TrimSpace(guard.Authorize)
	if guard.Authorize == "" {
		guard.Authorize = DefaultGuardHook
	}
	guard.Complete = strings.TrimSpace(guard.Complete)
	if guard.Priority < math.MinInt32 || guard.Priority > math.MaxInt32 {
		return fmt.Errorf("plugin meta guard priority must be a signed 32-bit integer")
	}
	if guard.TimeoutMs < 0 {
		return fmt.Errorf("plugin meta guard timeoutMs must be positive")
	}
	for index, method := range guard.Methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, method) {
			return fmt.Errorf("plugin meta guard methods must be HTTP methods")
		}
		guard.Methods[index] = method
	}
	for _, list := range [][]string{guard.Methods, guard.Paths, guard.ExcludePaths, guard.Models, guard.Groups} {
		if len(list) > MaxGuardScopeEntries {
			return fmt.Errorf("plugin meta guard scope lists must not exceed %d entries", MaxGuardScopeEntries)
		}
	}
	seenPaths := make(map[string]struct{}, len(guard.Paths))
	for _, path := range guard.Paths {
		if err := validateGuardPath(path); err != nil {
			return err
		}
		if _, duplicate := seenPaths[path]; duplicate {
			return fmt.Errorf("plugin meta guard paths must be unique")
		}
		seenPaths[path] = struct{}{}
	}
	for _, path := range guard.ExcludePaths {
		if err := validateGuardPath(path); err != nil {
			return err
		}
	}
	for _, group := range guard.Groups {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("plugin meta guard groups must contain non-empty names")
		}
	}
	for _, model := range guard.Models {
		if strings.TrimSpace(model) != model || model == "" {
			return fmt.Errorf("plugin meta guard models must contain non-empty canonical names")
		}
	}
	if len(guard.ConfigFields) > MaxGuardConfigFields {
		return fmt.Errorf("plugin meta guard configFields must not exceed %d entries", MaxGuardConfigFields)
	}
	seenFields := make(map[string]struct{}, len(guard.ConfigFields))
	for index := range guard.ConfigFields {
		field := &guard.ConfigFields[index]
		field.Name = strings.TrimSpace(field.Name)
		if field.Name == "" || len(field.Name) > MaxGuardConfigFieldName {
			return fmt.Errorf("plugin meta guard configFields names must be 1 to %d characters", MaxGuardConfigFieldName)
		}
		if _, duplicate := seenFields[field.Name]; duplicate {
			return fmt.Errorf("plugin meta guard configFields must be unique")
		}
		seenFields[field.Name] = struct{}{}
		if !slices.Contains(guardConfigFieldTypes, field.Type) {
			return fmt.Errorf("plugin meta guard configFields[%d] type must be one of %s", index, strings.Join(guardConfigFieldTypes, ", "))
		}
		if field.Type == "enum" && len(field.EnumValues) == 0 {
			return fmt.Errorf("plugin meta guard configFields[%d] of type enum must declare enumValues", index)
		}
		if field.Type != "enum" && len(field.EnumValues) > 0 {
			return fmt.Errorf("plugin meta guard configFields[%d] declares enumValues but is not of type enum", index)
		}
	}
	return nil
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

func validateGuardPath(path string) error {
	if path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("plugin meta guard paths must start with /")
	}
	if len(path) > MaxGuardPathLength {
		return fmt.Errorf("plugin meta guard paths must not exceed %d characters", MaxGuardPathLength)
	}
	if strings.ContainsAny(path, "?# \t\r\n") {
		return fmt.Errorf("plugin meta guard paths must not contain a query, fragment, or whitespace")
	}
	return nil
}

// GuardTimeout bounds one guard hook call from the block's timeoutMs.
func GuardTimeout(guard GuardMeta) time.Duration {
	if guard.TimeoutMs <= 0 {
		return DefaultGuardTimeout
	}
	return min(max(time.Duration(guard.TimeoutMs)*time.Millisecond, MinGuardTimeout), MaxGuardTimeout)
}

// InterceptorTimeout bounds one interceptor hook call from the block's
// timeoutMs.
func InterceptorTimeout(meta InterceptorMeta) time.Duration {
	if meta.TimeoutMs <= 0 {
		return DefaultInterceptorTimeout
	}
	return min(max(time.Duration(meta.TimeoutMs)*time.Millisecond, MinInterceptorTimeout), MaxInterceptorTimeout)
}

// cloneGuardConfigFields copies a configFields slice for independent readers.
func cloneGuardConfigFields(fields []GuardConfigField) []GuardConfigField {
	cloned := append([]GuardConfigField(nil), fields...)
	for index := range cloned {
		cloned[index].EnumValues = slices.Clone(cloned[index].EnumValues)
		cloned[index].Description = cloned[index].Description.Clone()
	}
	return cloned
}

// Clone returns a copy of the localized text that shares no map with it.
func (t LocalizedText) Clone() LocalizedText {
	if t == nil {
		return nil
	}
	cloned := make(LocalizedText, len(t))
	for key, value := range t {
		cloned[key] = value
	}
	return cloned
}

// verifyGuardExports checks that every hook the block binds to exists.
func verifyGuardExports(engine *Engine, meta Meta) error {
	for _, hook := range []struct {
		name string
		bind string
	}{
		{"decision hook", meta.Guard.Authorize},
		{"completion hook", meta.Guard.Complete},
	} {
		if hook.bind == "" {
			continue
		}
		callable, err := engine.HasCallablePath(context.Background(), hook.bind)
		if err != nil {
			return err
		}
		if !callable {
			if hook.bind == meta.Guard.Authorize {
				return fmt.Errorf("plugin %s declares meta.guard but does not export %q", meta.Key, hook.bind)
			}
			return fmt.Errorf("plugin %s declares meta.guard complete hook %q but does not export it", meta.Key, hook.bind)
		}
	}
	return nil
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
