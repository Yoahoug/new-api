package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const guardAllowAllSource = `
export const meta = {
  apiVersion: 1, key: "guard-allow-all", name: "Allow All", version: "1.0.0",
  author: {name: "Test"}, requiredCapabilities: ["request-guard@1"],
  guard: {priority: 10, complete: "onComplete"},
};
export function authorize(ctx) { return {allow: true, metadata: {userId: String(ctx.userId)}}; }
export function onComplete() { return {allow: true}; }
`

const guardDenySource = `
export const meta = {
  apiVersion: 1, key: "guard-deny", name: "Deny", version: "1.0.0",
  author: {name: "Test"}, requiredCapabilities: ["request-guard@1"],
  guard: {priority: 10, models: ["gpt-4o"]},
};
export function authorize(ctx) {
  if (ctx.usingGroup === "banned") {
    return {allow: false, status: 403, code: "group_banned", message: "group " + ctx.usingGroup + " is banned", headers: {"X-Guard": "deny"}};
  }
  return {allow: true};
}
`

const guardDenyEveryModelSource = `
export const meta = {
  apiVersion: 1, key: "guard-deny-models", name: "Deny Models", version: "1.0.0",
  author: {name: "Test"}, requiredCapabilities: ["request-guard@1"],
  guard: {priority: 0, models: ["gpt-4o"]},
};
export function authorize(ctx) { return {allow: false, message: "model " + ctx.model + " is not permitted"}; }
`

// installRequestGuard registers a guard for the duration of one test and
// restores the previous registry contents afterwards.
func installRequestGuard(t *testing.T, source string) {
	t.Helper()
	_, err := jsplugin.DefaultRegistry.Register(source, jsplugin.Options{})
	require.NoError(t, err)
	key := guardKeyOf(t, source)
	t.Cleanup(func() { jsplugin.DefaultRegistry.Unregister(key) })
}

func guardKeyOf(t *testing.T, source string) string {
	t.Helper()
	for _, line := range strings.Split(source, "\n") {
		if index := strings.Index(line, `key: "`); index >= 0 {
			rest := line[index+len(`key: "`):]
			return rest[:strings.Index(rest, `"`)]
		}
	}
	t.Fatalf("guard source has no key")
	return ""
}

// runGuardedRequest drives one relay-shaped request through the real handler
// chain — authenticated context, body storage, guard, then the handler that
// stands in for distribution — and reports whether that last handler ran.
func runGuardedRequest(t *testing.T, body string, applyContext func(c *gin.Context)) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	reached := false
	engine.POST("/v1/chat/completions",
		func(c *gin.Context) {
			c.Set(common.RequestIdKey, "req-guard-1")
			if applyContext != nil {
				applyContext(c)
			}
			storage, err := common.GetRequestBody(c)
			require.NoError(t, err)
			require.NotNil(t, storage)
			c.Next()
		},
		PluginRequestGuard(),
		func(c *gin.Context) {
			reached = true
			c.JSON(http.StatusOK, gin.H{"ok": true})
		},
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer sk-secret")
	request.Header.Set("X-Vendor-Api-Key", "vendor-secret")
	engine.ServeHTTP(recorder, request)
	return recorder, reached
}

func TestInstalledRequestGuardAllowsEverythingUntilConfigured(t *testing.T) {
	// The built-in model-policy guard ships enabled. A gateway that has never
	// configured it must keep serving every request, so shipping the guard
	// cannot change the behavior of an existing deployment.
	guards := jsplugin.DefaultRegistry.Generation().RequestGuards()
	require.NotEmpty(t, guards, "the built-in request guard must be installed")
	require.NoError(t, jsplugin.SetGuardConfigsOption(""))
	t.Cleanup(func() { _ = jsplugin.SetGuardConfigsOption("") })

	recorder, reachedHandler := runGuardedRequest(t, `{"model":"gpt-4o"}`, func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUserId, 7)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})

	assert.True(t, reachedHandler)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestPluginRequestGuardPassesAnAllowedRequestThrough(t *testing.T) {
	installRequestGuard(t, guardAllowAllSource)
	recorder, reachedHandler := runGuardedRequest(t, `{"model":"gpt-4o"}`, func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUserId, 7)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})

	assert.True(t, reachedHandler)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestPluginRequestGuardDeniesBeforeDistribution(t *testing.T) {
	installRequestGuard(t, guardDenySource)
	recorder, reachedHandler := runGuardedRequest(t, `{"model":"gpt-4o"}`, func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUserId, 7)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "banned")
	})

	assert.False(t, reachedHandler, "a denied request must never reach distribution")
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, "deny", recorder.Header().Get("X-Guard"))
	assert.Contains(t, recorder.Body.String(), `"code":"group_banned"`)
	assert.Contains(t, recorder.Body.String(), "group banned is banned")
	assert.Contains(t, recorder.Body.String(), "req-guard-1", "the refusal carries the request id")
}

func TestPluginRequestGuardResolvesModelAndGroupFromTheRequest(t *testing.T) {
	installRequestGuard(t, guardDenyEveryModelSource)

	denied, reached := runGuardedRequest(t, `{"model":"gpt-4o","stream":true}`, func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUserId, 7)
	})
	assert.False(t, reached)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Contains(t, denied.Body.String(), "gpt-4o is not permitted", "the guard sees the requested model")

	allowed, reached := runGuardedRequest(t, `{"model":"claude-sonnet-4"}`, func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUserId, 7)
	})
	assert.True(t, reached)
	assert.Equal(t, http.StatusOK, allowed.Code)
}

func TestPluginRequestGuardHidesCredentialHeaders(t *testing.T) {
	installRequestGuard(t, `
export const meta = {
  apiVersion: 1, key: "guard-header-audit", name: "Header Audit", version: "1.0.0",
  author: {name: "Test"}, requiredCapabilities: ["request-guard@1"], guard: {},
};
export function authorize(ctx) {
  const leaked = Object.keys(ctx.headers).filter(function (name) {
    return name === "authorization" || name === "x-vendor-api-key";
  });
  return {allow: true, metadata: {leaked: leaked.join(",") || "none"}};
}
`)

	recorder, reachedHandler := runGuardedRequest(t, `{"model":"gpt-4o"}`, nil)
	assert.True(t, reachedHandler)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestPluginRequestGuardLeavesTheBodyReadableForTheRelay(t *testing.T) {
	installRequestGuard(t, guardAllowAllSource)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/v1/chat/completions",
		func(c *gin.Context) {
			_, err := common.GetRequestBody(c)
			require.NoError(t, err)
			c.Next()
		},
		PluginRequestGuard(),
		func(c *gin.Context) {
			var body map[string]any
			require.NoError(t, common.UnmarshalBodyReusable(c, &body))
			c.JSON(http.StatusOK, body)
		},
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "messages", "the guard must not consume the request body")
}
