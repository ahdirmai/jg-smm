package http

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	openapiTypes "github.com/oapi-codegen/runtime/types"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

const (
	accessCookieName  = "smm_at"
	refreshCookieName = "smm_rt"

	// contextKeys
	ctxKeyClaims = "auth.claims"
)

// AuthHandler exposes login/refresh/logout/me and the RBAC middleware.
type AuthHandler struct {
	auth      *service.AuthService
	cookieTLS bool // set Secure on cookies when served over HTTPS
}

// NewAuthHandler builds the handler. cookieTLS enables the Secure cookie flag.
func NewAuthHandler(auth *service.AuthService, cookieTLS bool) *AuthHandler {
	return &AuthHandler{auth: auth, cookieTLS: cookieTLS}
}

// Register mounts public auth routes (no RBAC) on the router.
func (h *AuthHandler) Register(e *echo.Echo) {
	e.POST("/api/auth/login", h.login)
	e.POST("/api/auth/refresh", h.refresh)
	e.POST("/api/auth/logout", h.logout)
	e.GET("/api/auth/me", h.me, h.requireAuth())
}

type loginRequest = oapigen.LoginRequest

func (h *AuthHandler) login(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return domain.ErrValidation
	}
	sess, err := h.auth.Login(c.Request().Context(), string(req.Email), req.Password, c.Request().UserAgent(), c.RealIP())
	if err != nil {
		return err
	}
	h.setCookies(c, sess)
	return c.JSON(http.StatusOK, loginResponse(sess))
}

func (h *AuthHandler) refresh(c echo.Context) error {
	cookie, err := c.Cookie(refreshCookieName)
	if err != nil || cookie.Value == "" {
		return domain.ErrUnauthorized
	}
	sess, err := h.auth.Refresh(c.Request().Context(), cookie.Value, c.Request().UserAgent(), c.RealIP())
	if err != nil {
		h.clearCookies(c)
		return err
	}
	h.setCookies(c, sess)
	return c.JSON(http.StatusOK, loginResponse(sess))
}

func (h *AuthHandler) logout(c echo.Context) error {
	if cookie, err := c.Cookie(refreshCookieName); err == nil {
		if err := h.auth.Logout(c.Request().Context(), cookie.Value); err != nil {
			return err
		}
	}
	h.clearCookies(c)
	return c.NoContent(http.StatusNoContent)
}

func (h *AuthHandler) me(c echo.Context) error {
	claims, ok := ClaimsFrom(c)
	if !ok {
		return domain.ErrUnauthorized
	}
	return c.JSON(http.StatusOK, oapigen.MeResponse{UserId: claims.UserID, Role: oapigen.Role(claims.Role)})
}

// setCookies writes the access + refresh cookies. Access is short-lived and
// readable only by the server; refresh is path-scoped to the auth endpoints.
func (h *AuthHandler) setCookies(c echo.Context, sess service.Session) {
	c.SetCookie(&http.Cookie{
		Name:     accessCookieName,
		Value:    sess.AccessToken,
		Path:     "/",
		MaxAge:   int(service.AccessTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cookieTLS,
		SameSite: http.SameSiteStrictMode,
	})
	c.SetCookie(&http.Cookie{
		Name:     refreshCookieName,
		Value:    sess.RefreshToken,
		Path:     "/api/auth",
		MaxAge:   int(service.RefreshTokenTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.cookieTLS,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *AuthHandler) clearCookies(c echo.Context) {
	for _, name := range []string{accessCookieName, refreshCookieName} {
		c.SetCookie(&http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
			Secure:   h.cookieTLS,
			SameSite: http.SameSiteStrictMode,
		})
	}
}

// requireAuth validates the access cookie and stores claims in the context.
func (h *AuthHandler) requireAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cookie, err := c.Cookie(accessCookieName)
			if err != nil || cookie.Value == "" {
				return domain.ErrUnauthorized
			}
			claims, err := h.auth.VerifyAccessToken(cookie.Value)
			if err != nil {
				return err
			}
			c.Set(ctxKeyClaims, claims)
			return next(c)
		}
	}
}

// RequirePermission returns middleware that rejects callers whose role lacks
// the permission. It must be mounted after requireAuth.
func RequirePermission(p domain.Permission) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			claims, ok := ClaimsFrom(c)
			if !ok {
				return domain.ErrUnauthorized
			}
			if !claims.Role.Can(p) {
				return domain.ErrForbidden
			}
			return next(c)
		}
	}
}

// ClaimsFrom returns the verified claims stored by requireAuth.
func ClaimsFrom(c echo.Context) (port.Claims, bool) {
	v, ok := c.Get(ctxKeyClaims).(port.Claims)
	return v, ok
}

func toUserResponse(u port.User) oapigen.User {
	return oapigen.User{Id: u.ID, Email: openapiTypes.Email(u.Email), Name: u.Name, Role: oapigen.Role(u.Role)}
}

// loginResponse builds the generated response shape from a session.
func loginResponse(sess service.Session) oapigen.LoginResponse {
	return oapigen.LoginResponse{AccessExpiry: sess.AccessExpiry, User: toUserResponse(sess.User)}
}
