package channel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	common2 "github.com/QuantumNous/new-api/common"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const interceptorHeaderPluginSource = `
export const meta = {
	apiVersion: 1, key: "int-header-smoke", name: "Header Smoke", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-interceptor@1"],
	interceptor: {intercept: "intercept"},
};
export function intercept(ctx) {
	return {
		body: null,
		headers: {"X-OpenCode-Session": "sess-" + ctx.upstreamModel},
	};
}
`

const interceptorBodyPluginSource = `
export const meta = {
	apiVersion: 1, key: "int-body-smoke", name: "Body Smoke", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-interceptor@1"],
	interceptor: {intercept: "intercept"},
};
export function intercept(ctx) {
	if (!ctx.body) {
		return {body: null};
	}
	return {
		body: Object.assign({}, ctx.body, {patched: true}),
	};
}
`

func registerInterceptor(t *testing.T, source string) {
	t.Helper()
	_, err := pluginruntime.DefaultRegistry.Register(source, pluginruntime.Options{})
	require.NoError(t, err)
	for _, key := range []string{"int-header-smoke", "int-body-smoke"} {
		if strings.Contains(source, `key: "`+key+`"`) {
			t.Cleanup(func() { pluginruntime.DefaultRegistry.Unregister(key) })
		}
	}
}

// interceptorRelayInfo builds a RelayInfo just deep enough for the outbound
// dispatcher: channel metadata, the upstream model, and the user identity.
func interceptorRelayInfo() *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelId:         7,
		ChannelType:       1,
		ChannelBaseUrl:    "https://upstream.example",
		UpstreamModelName: "vendor-endpoint-1",
	}
	info.OriginModelName = "gpt-4o"
	info.IsStream = true
	info.UsingGroup = "default"
	info.UserId = 42
	return info
}

func TestApplyPluginRequestInterceptorsInjectsHeader(t *testing.T) {
	registerInterceptor(t, interceptorHeaderPluginSource)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(common2.RequestIdKey, "req-int-e2e")
	_, err := common2.GetRequestBody(c)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer channel-key")

	require.NoError(t, applyPluginRequestInterceptors(c, req, interceptorRelayInfo()))

	assert.Equal(t, "sess-vendor-endpoint-1", req.Header.Get("X-OpenCode-Session"))
	assert.Equal(t, "Bearer channel-key", req.Header.Get("Authorization"), "the host credential must survive the chain")
}

func TestApplyPluginRequestInterceptorsReplacesBody(t *testing.T) {
	registerInterceptor(t, interceptorBodyPluginSource)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(common2.RequestIdKey, "req-int-e2e")
	_, err := common2.GetRequestBody(c)
	require.NoError(t, err)

	payload, closer, err := relaycommon.NewOutboundJSONBody([]byte(`{"model":"gpt-4o"}`))
	require.NoError(t, err)
	defer closer.Close()
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", payload)
	require.NoError(t, err)
	ApplyUpstreamBodyMetadata(req, payload)

	require.NoError(t, applyPluginRequestInterceptors(c, req, interceptorRelayInfo()))

	rewritten, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Contains(t, string(rewritten), `"patched":true`)
	assert.Contains(t, string(rewritten), `"messages"`, "the rest of the body must survive the rewrite")
	assert.EqualValues(t, len(rewritten), req.ContentLength)
	replayed, err := req.GetBody()
	require.NoError(t, err)
	replayedBytes, err := io.ReadAll(replayed)
	require.NoError(t, err)
	assert.Equal(t, rewritten, replayedBytes, "the replacement body must stay replayable for retries")
}

func TestApplyPluginRequestInterceptorsIsANoOpWithoutInterceptors(t *testing.T) {
	t.Cleanup(func() { pluginruntime.DefaultRegistry.Unregister("int-absent") })
	require.NoError(t, pluginruntime.DefaultRegistry.Unregister("int-absent"))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", nil)
	require.NoError(t, err)
	assert.NoError(t, applyPluginRequestInterceptors(c, req, interceptorRelayInfo()))
}

func TestApplyPluginRequestInterceptorsSkipsChannelTests(t *testing.T) {
	registerInterceptor(t, interceptorHeaderPluginSource)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := interceptorRelayInfo()
	info.IsChannelTest = true
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", nil)
	require.NoError(t, err)
	require.NoError(t, applyPluginRequestInterceptors(c, req, info))
	assert.Empty(t, req.Header.Get("X-OpenCode-Session"))
}

func TestApplyPluginRequestInterceptorsFailsClosedOnBrokenHook(t *testing.T) {
	registerInterceptor(t, `
export const meta = {
	apiVersion: 1, key: "int-body-smoke", name: "Body Smoke", version: "1.0.0",
	author: {name: "Test"}, requiredCapabilities: ["request-interceptor@1"],
	interceptor: {intercept: "intercept"},
};
export function intercept() { throw new Error("boom"); }
`)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(common2.RequestIdKey, "req-int-e2e")
	_, err := common2.GetRequestBody(c)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", nil)
	require.NoError(t, err)
	apiErr := applyPluginRequestInterceptors(c, req, interceptorRelayInfo())
	require.Error(t, apiErr)
	newAPIError, isAPIError := apiErr.(*types.NewAPIError)
	require.True(t, isAPIError)
	assert.Equal(t, types.ErrorCodeDoRequestFailed, newAPIError.GetErrorCode())
}
