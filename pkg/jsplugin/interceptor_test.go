package jsplugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interceptTestSource builds a request interceptor plugin whose hook body is
// supplied by the test, so each case states its rewrite intent inline.
func interceptTestSource(key, block, hooks string) string {
	return `export const meta = {
	apiVersion: 1, key: "` + key + `", name: "` + key + `", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-interceptor@1"],
	interceptor: {` + block + `},
};
` + hooks
}

const interceptNoop = `export function intercept() { return {body: null}; }`

func interceptRequest() InterceptRequest {
	return InterceptRequest{
		RequestID:       "req-int-1",
		Method:          "POST",
		Path:            "/v1/chat/completions",
		UpstreamBaseURL: "https://upstream.example",
		UpstreamModel:   "gpt-4o",
		OriginModel:     "gpt-4o",
		Stream:          true,
		UsingGroup:      "default",
		Headers:         map[string]string{"content-type": "application/json"},
		Body:            map[string]any{"model": "gpt-4o"},
	}
}

func TestInterceptorManifestRequiresCapabilityAndExcludesOtherSurfaces(t *testing.T) {
	_, err := NewRegistry().Register(`
export const meta = {apiVersion: 1, key: "int-no-cap", name: "NoCap", version: "1.0.0", author: {name: "Test"}, interceptor: {}};
export function intercept() { return {body: null}; }
`, Options{})
	require.ErrorContains(t, err, "must require the request-interceptor@1 capability")

	_, err = NewRegistry().Register(`
export const meta = {apiVersion: 1, key: "int-models", name: "Models", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-interceptor@1"],
	models: ["gpt-4o"], interceptor: {}};
export function intercept() { return {body: null}; }
`, Options{})
	require.ErrorContains(t, err, "must not declare models")

	_, err = NewRegistry().Register(`
export const meta = {apiVersion: 1, key: "int-both", name: "Both", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-interceptor@1", "request-guard@1"],
	guard: {}, interceptor: {}};
export function intercept() { return {body: null}; }
export function authorize() { return {allow: true}; }
`, Options{})
	require.ErrorContains(t, err, "cannot declare both guard and interceptor")
}

func TestInterceptorRewritesBodyAndHeaders(t *testing.T) {
	registry := guardTestRegistry(t, interceptTestSource("shim", `priority: 5`, `
export function intercept(ctx) {
	return {
		body: {model: ctx.upstreamModel, messages: ctx.body.messages, patched: true},
		headers: {"X-OpenCode-Session": "sess-123"},
	};
}
`))
	outcome := registry.Generation().RunOutboundInterceptors(t.Context(), interceptRequest())

	assert.False(t, outcome.Failed)
	require.NotNil(t, outcome.Body)
	assert.Contains(t, string(outcome.Body), `"patched":true`)
	assert.Equal(t, map[string]string{"X-OpenCode-Session": "sess-123"}, outcome.Headers)
	assert.Equal(t, "shim", outcome.Key)
}

func TestInterceptorNoopKeepsTheOriginalBody(t *testing.T) {
	registry := guardTestRegistry(t, interceptTestSource("noop", ``, interceptNoop))
	outcome := registry.Generation().RunOutboundInterceptors(t.Context(), interceptRequest())

	assert.False(t, outcome.Failed)
	assert.Nil(t, outcome.Body)
	assert.Nil(t, outcome.Headers)
}

func TestInterceptorChainComposesRewrites(t *testing.T) {
	registry := guardTestRegistry(t,
		interceptTestSource("first", `priority: 10`, `
export function intercept(ctx) {
	return {body: Object.assign({}, ctx.body, {step: 1})};
}
`),
		interceptTestSource("second", `priority: 1`, `
export function intercept(ctx) {
	return {body: Object.assign({}, ctx.body, {step: ctx.body.step + 1})};
}
`),
	)
	outcome := registry.Generation().RunOutboundInterceptors(t.Context(), interceptRequest())

	assert.Contains(t, string(outcome.Body), `"step":2`, "the second hook must see the first hook's rewrite")
}

func TestInterceptorScopeFollowsUpstreamModelAndUrl(t *testing.T) {
	scoped := guardTestRegistry(t, interceptTestSource("scoped-int", `
	methods: ["POST"], models: ["gpt-4o"], paths: ["/v1/"],
	`, `export function intercept() { return {body: {patched: true}}; }`))

	cases := []struct {
		name     string
		mutate   func(*InterceptRequest)
		rewrites bool
	}{
		{"claimed", func(*InterceptRequest) {}, true},
		{"other model", func(r *InterceptRequest) { r.UpstreamModel = "other" }, false},
		{"unresolved model", func(r *InterceptRequest) { r.UpstreamModel = "" }, false},
		{"other path", func(r *InterceptRequest) { r.Path = "/pg/chat" }, false},
		{"other method", func(r *InterceptRequest) { r.Method = "GET" }, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := interceptRequest()
			testCase.mutate(&request)
			outcome := scoped.Generation().RunOutboundInterceptors(t.Context(), request)
			assert.Equal(t, testCase.rewrites, outcome.Body != nil)
		})
	}
}

func TestInterceptorFailsClosedUnlessFailOpen(t *testing.T) {
	closed := guardTestRegistry(t, interceptTestSource("broken-int", ``, `export function intercept() { throw new Error("boom"); }`))
	outcome := closed.Generation().RunOutboundInterceptors(t.Context(), interceptRequest())
	assert.True(t, outcome.Failed)
	assert.Contains(t, outcome.Error.Error(), "broken-int")

	open := guardTestRegistry(t, interceptTestSource("broken-open", `failOpen: true`, `export function intercept() { throw new Error("boom"); }`))
	assert.False(t, open.Generation().RunOutboundInterceptors(t.Context(), interceptRequest()).Failed)
}

func TestInterceptorCannotRewriteHostOwnedHeaders(t *testing.T) {
	registry := guardTestRegistry(t, interceptTestSource("hostile", ``, `
export function intercept() {
	return {headers: {"Authorization": "Bearer stolen", "X-Fine": "ok"}};
}
`))
	outcome := registry.Generation().RunOutboundInterceptors(t.Context(), interceptRequest())
	assert.True(t, outcome.Failed)
	assert.Contains(t, outcome.Error.Error(), "Authorization")
}

func TestInterceptorRejectsNonObjectBody(t *testing.T) {
	registry := guardTestRegistry(t, interceptTestSource("bad-body", ``, `export function intercept() { return {body: "not json"}; }`))
	outcome := registry.Generation().RunOutboundInterceptors(t.Context(), interceptRequest())
	assert.True(t, outcome.Failed)
	assert.Contains(t, outcome.Error.Error(), "JSON object")
}

func TestInterceptorReceivesAdministratorConfig(t *testing.T) {
	require.NoError(t, SetInterceptorConfigsOption(`{"cfg-int":{"sessionHeader":"X-Session"}}`))
	t.Cleanup(func() { _ = SetInterceptorConfigsOption("") })

	registry := guardTestRegistry(t, interceptTestSource("cfg-int", ``, `
export function intercept(ctx) {
	return {headers: {[ctx.config.sessionHeader]: "1"}};
}
`))
	outcome := registry.Generation().RunOutboundInterceptors(t.Context(), interceptRequest())
	assert.Equal(t, map[string]string{"X-Session": "1"}, outcome.Headers)
}

func TestInterceptorHotReload(t *testing.T) {
	registry := NewRegistry()
	require.NoError(t, registry.ReplaceOverrides(nil))
	assert.False(t, registry.Generation().HasOutboundInterceptors())

	_, err := registry.Register(interceptTestSource("reload-int", ``, interceptNoop), Options{})
	require.NoError(t, err)
	assert.True(t, registry.Generation().HasOutboundInterceptors())
	assert.Len(t, registry.Generation().OutboundInterceptors(), 1)
}
