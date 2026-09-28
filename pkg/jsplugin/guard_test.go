package jsplugin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardTestSource builds a request guard plugin whose decision hook returns a
// fixed literal, so a test states its intent in the expectation rather than in
// plugin logic.
func guardTestSource(key, guardBlock, hooks string) string {
	source := `export const meta = {
	apiVersion: 1, key: "` + key + `", name: "` + key + `", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-guard@1"],
	guard: {` + guardBlock + `},
};
`
	return source + hooks
}

const allowEverything = `export function authorize() { return {allow: true}; }`

func guardTestRegistry(t *testing.T, sources ...string) *Registry {
	t.Helper()
	registry := NewRegistry()
	for _, source := range sources {
		_, err := registry.Register(source, Options{})
		require.NoError(t, err)
	}
	return registry
}

func guardRequest() GuardRequest {
	return GuardRequest{
		RequestID: "req-1",
		UnixNow:   1700000000,
		Method:    "POST",
		Path:      "/v1/chat/completions",
		ClientIP:  "203.0.113.7",
		UserID:    42,
		TokenID:   9,
		UserGroup: "default",
		Model:     "gpt-4o-mini",
		Headers:   map[string]string{"content-type": "application/json"},
	}
}

func TestGuardManifestRequiresCapabilityAndExcludesTaskPluginSurface(t *testing.T) {
	_, err := NewRegistry().Register(`
export const meta = {apiVersion: 1, key: "no-cap", name: "NoCap", version: "1.0.0", author: {name: "Test"}, guard: {}};
export function authorize() { return {allow: true}; }
`, Options{})
	require.ErrorContains(t, err, "must require the request-guard@1 capability")

	_, err = NewRegistry().Register(`
export const meta = {apiVersion: 1, key: "with-models", name: "WithModels", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-guard@1"],
	models: ["gpt-4o"], guard: {}};
export function authorize() { return {allow: true}; }
`, Options{})
	require.ErrorContains(t, err, "must not declare models")

	_, err = NewRegistry().Register(`
export const meta = {apiVersion: 1, key: "with-routes", name: "WithRoutes", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-guard@1"],
	routes: [{method: "POST", path: "/x/y", type: "submit", decode: "a", render: "b"}], guard: {}};
export function authorize() { return {allow: true}; }
`, Options{})
	require.ErrorContains(t, err, "must not declare routes or protocols")
}

func TestGuardManifestRejectsUnknownFieldAndMissingHook(t *testing.T) {
	_, err := NewRegistry().Register(guardTestSource("typo", `priorty: 10`, allowEverything), Options{})
	require.ErrorContains(t, err, `guard has unknown field "priorty"`)

	_, err = NewRegistry().Register(guardTestSource("missing", "", ""), Options{})
	require.ErrorContains(t, err, `does not export "authorize"`)
}

func TestGuardManifestRejectsUnusableScope(t *testing.T) {
	_, err := NewRegistry().Register(guardTestSource("bad-method", `methods: ["FETCH"]`, allowEverything), Options{})
	require.ErrorContains(t, err, "guard methods must be HTTP methods")

	_, err = NewRegistry().Register(guardTestSource("bad-path", `paths: ["v1/chat"]`, allowEverything), Options{})
	require.ErrorContains(t, err, "guard paths must start with /")

	_, err = NewRegistry().Register(guardTestSource("bad-config", ``, allowEverything), Options{})
	require.NoError(t, err, "a guard with no configFields is valid")

	_, err = NewRegistry().Register(guardTestSource(
		"bad-enum",
		`configFields: [{name: "mode", type: "enum"}]`,
		allowEverything,
	), Options{})
	require.ErrorContains(t, err, "must declare enumValues")
}

func TestGuardChainOrdersByPriorityThenKey(t *testing.T) {
	order := guardTestRegistry(t,
		guardTestSource("low", `priority: 1`, `export function authorize() { return {allow: true, metadata: {ran: "low"}}; }`),
		guardTestSource("high", `priority: 9`, `export function authorize() { return {allow: true, metadata: {ran: "high"}}; }`),
		guardTestSource("tie-b", `priority: 5`, `export function authorize() { return {allow: false, message: "tie-b"}; }`),
		guardTestSource("tie-a", `priority: 5`, `export function authorize() { return {allow: true}; }`),
	)
	guards := order.Generation().RequestGuards()
	require.Len(t, guards, 4)
	assert.Equal(t, []string{"high", "tie-a", "tie-b", "low"}, []string{
		guards[0].Meta.Key, guards[1].Meta.Key, guards[2].Meta.Key, guards[3].Meta.Key,
	})
}

func TestGuardChainStopsAtFirstDenial(t *testing.T) {
	registry := guardTestRegistry(t,
		guardTestSource("first", `priority: 10`, `export function authorize() { return {allow: false, status: 429, code: "rate_limited", message: "slow down", headers: {"Retry-After": "1"}}; }`),
		guardTestSource("second", `priority: 5`, `export function authorize() { return {allow: false, message: "never reached"}; }`),
	)
	outcome := registry.Generation().RunRequestGuards(t.Context(), guardRequest())

	assert.False(t, outcome.Allowed)
	assert.Equal(t, "first", outcome.Key)
	assert.False(t, outcome.Failed)
	assert.Equal(t, 429, outcome.Status)
	assert.Equal(t, "rate_limited", outcome.Code)
	assert.Equal(t, "slow down", outcome.Message)
	assert.Equal(t, map[string]string{"Retry-After": "1"}, outcome.Headers)
}

func TestGuardExclusiveAllowEndsTheChain(t *testing.T) {
	registry := guardTestRegistry(t,
		guardTestSource("gate", `priority: 10, exclusive: true`, `export function authorize() { return {allow: true}; }`),
		guardTestSource("later", `priority: 1`, `export function authorize() { return {allow: false, message: "must not run"}; }`),
	)
	assert.True(t, registry.Generation().RunRequestGuards(t.Context(), guardRequest()).Allowed)

	blocking := guardTestRegistry(t,
		guardTestSource("gate", `exclusive: true`, `export function authorize() { return {allow: false, message: "closed"}; }`),
		guardTestSource("earlier", `priority: -1`, `export function authorize() { return {allow: true}; }`),
	)
	outcome := blocking.Generation().RunRequestGuards(t.Context(), guardRequest())
	assert.False(t, outcome.Allowed)
	assert.Equal(t, "gate", outcome.Key)
}

func TestGuardScopeSelectsWhichRequestsAPluginSees(t *testing.T) {
	scoped := guardTestRegistry(t, guardTestSource("scoped", `
	priority: 0, methods: ["POST"], paths: ["/v1/"], excludePaths: ["/v1/models"],
	models: ["GPT-4O-Mini"], groups: ["default", "vip"],
	`, `export function authorize() { return {allow: false, message: "scoped"}; }`))

	cases := []struct {
		name    string
		mutate  func(*GuardRequest)
		blocked bool
	}{
		{"claimed", func(*GuardRequest) {}, true},
		{"other method", func(r *GuardRequest) { r.Method = "GET" }, false},
		{"other path", func(r *GuardRequest) { r.Path = "/pg/chat/completions" }, false},
		{"excluded path", func(r *GuardRequest) { r.Path = "/v1/models/gpt-4o-mini" }, false},
		{"other model", func(r *GuardRequest) { r.Model = "gpt-4o" }, false},
		{"unresolved model", func(r *GuardRequest) { r.Model = "" }, false},
		{"other group", func(r *GuardRequest) { r.UserGroup = "trial" }, false},
		{"token group wins", func(r *GuardRequest) { r.UsingGroup = "vip" }, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := guardRequest()
			testCase.mutate(&request)
			outcome := scoped.Generation().RunRequestGuards(t.Context(), request)
			assert.Equal(t, !testCase.blocked, outcome.Allowed)
		})
	}
}

func TestGuardFailsClosedOnHookErrorUnlessFailOpen(t *testing.T) {
	closed := guardTestRegistry(t, guardTestSource("broken", ``, `export function authorize() { throw new Error("boom"); }`))
	outcome := closed.Generation().RunRequestGuards(t.Context(), guardRequest())
	assert.False(t, outcome.Allowed)
	assert.True(t, outcome.Failed)
	assert.Equal(t, "permission_guard_error", outcome.Code)
	assert.Equal(t, 403, outcome.Status)

	open := guardTestRegistry(t, guardTestSource("broken-open", `failOpen: true`, `export function authorize() { throw new Error("boom"); }`))
	assert.True(t, open.Generation().RunRequestGuards(t.Context(), guardRequest()).Allowed)
}

func TestGuardTimeoutIsBoundedAndFailureIsClosed(t *testing.T) {
	registry := guardTestRegistry(t, guardTestSource("slow", `timeoutMs: 100`, `export function authorize() { while (true) {} }`))
	started := time.Now()
	outcome := registry.Generation().RunRequestGuards(t.Context(), guardRequest())
	assert.False(t, outcome.Allowed)
	assert.True(t, outcome.Failed)
	assert.Less(t, time.Since(started), 5*time.Second, "the guard timeout must bound the request, not the host")
}

func TestGuardDecisionClampsStatusAndSuppliesDefaults(t *testing.T) {
	registry := guardTestRegistry(t, guardTestSource("odd", ``, `
export function authorize() {
	return {allow: false, status: 200, code: "", message: ""};
}
`))
	outcome := registry.Generation().RunRequestGuards(t.Context(), guardRequest())
	assert.False(t, outcome.Allowed)
	assert.Equal(t, 403, outcome.Status, "a status below 400 must not be served as a decision")
	assert.Equal(t, "permission_denied", outcome.Code)
	assert.Contains(t, outcome.Message, "odd", "a missing message names the deciding guard")
}

func TestGuardDecisionRefusesFramingHeaders(t *testing.T) {
	registry := guardTestRegistry(t, guardTestSource("framing", ``, `
export function authorize() {
	return {allow: false, headers: {"Content-Type": "text/html", "X-Reason": "policy"}};
}
`))
	outcome := registry.Generation().RunRequestGuards(t.Context(), guardRequest())
	assert.False(t, outcome.Allowed)
	assert.True(t, outcome.Failed, "a guard must not be able to reshape the denial response")
	assert.Contains(t, outcome.Metadata["guard_error"], "cannot be set by a request guard")
}

func TestGuardRejectsMalformedDecision(t *testing.T) {
	registry := guardTestRegistry(t, guardTestSource("malformed", ``, `export function authorize() { return "nope"; }`))
	outcome := registry.Generation().RunRequestGuards(t.Context(), guardRequest())
	assert.False(t, outcome.Allowed)
	assert.True(t, outcome.Failed)
	assert.Contains(t, outcome.Metadata["guard_error"], "boolean allow field")
}

func TestGuardReceivesAdministratorConfig(t *testing.T) {
	require.NoError(t, SetGuardConfigsOption(`{"scoped":{"allowed":["a"]}}`))
	t.Cleanup(func() { _ = SetGuardConfigsOption("") })

	registry := guardTestRegistry(t, guardTestSource("scoped", ``, `
export function authorize(ctx) {
	return {allow: ctx.config.allowed.indexOf("a") >= 0, metadata: {seen: ctx.config.allowed[0]}};
}
`))
	assert.Equal(t, map[string]any{"allowed": []any{"a"}}, GuardConfigsSnapshot()["scoped"])

	outcome := registry.Generation().RunRequestGuards(t.Context(), guardRequest())
	assert.True(t, outcome.Allowed)
}

func TestGuardConfigsRejectMalformedOptionWithoutDroppingTheSnapshot(t *testing.T) {
	require.NoError(t, SetGuardConfigsOption(`{"kept":{"a":1}}`))
	t.Cleanup(func() { _ = SetGuardConfigsOption("") })

	require.Error(t, SetGuardConfigsOption(`[1,2,3]`))
	assert.Contains(t, GuardConfigsSnapshot(), "kept", "a rejected update must not silently strip every rule")
}

func TestGuardIsNotBindableToAChannel(t *testing.T) {
	registry := guardTestRegistry(t, guardTestSource("scoped", ``, allowEverything))
	plugin, ok := registry.Get("scoped")
	require.True(t, ok)

	assert.True(t, plugin.Meta.IsRequestGuard())
	require.ErrorContains(t, plugin.Meta.TaskPluginBindableError(), "cannot be bound to a channel")

	taskPlugin := Meta{Key: "sora"}
	assert.False(t, taskPlugin.IsRequestGuard())
	assert.NoError(t, taskPlugin.TaskPluginBindableError())
}

func TestGuardWithoutAnyInstalledPluginIsAPassThrough(t *testing.T) {
	registry := NewRegistry()
	assert.False(t, registry.Generation().HasRequestGuards())
	assert.True(t, registry.Generation().RunRequestGuards(t.Context(), guardRequest()).Allowed)
	assert.Nil(t, registry.Generation().RequestGuards())
}

func TestGuardHotReloadPublishesWithoutRestart(t *testing.T) {
	// A guard reaches the request path through the same generation an upload
	// publishes, so this is the reload contract an administrator observes:
	// ReplaceOverrides is the single call behind the 30-second sync loop.
	registry := NewRegistry()
	require.NoError(t, registry.ReplaceOverrides(nil))
	assert.False(t, registry.Generation().HasRequestGuards())

	_, err := registry.Register(guardTestSource("reload", `priority: 5`,
		`export function authorize() { return {allow: false, message: "v1"}; }`), Options{})
	require.NoError(t, err)
	generation := registry.Generation()
	require.True(t, generation.HasRequestGuards())
	assert.False(t, generation.RunRequestGuards(t.Context(), guardRequest()).Allowed)

	_, err = registry.Register(guardTestSource("reload", `priority: 5`,
		`export function authorize() { return {allow: true}; }`), Options{})
	require.NoError(t, err)
	// The old generation stays internally consistent for requests already
	// pinned to it, while new requests see the new decision.
	assert.False(t, generation.RunRequestGuards(t.Context(), guardRequest()).Allowed)
	assert.True(t, registry.Generation().RunRequestGuards(t.Context(), guardRequest()).Allowed)
	assert.Len(t, registry.Generation().RequestGuards(), 1)
}
