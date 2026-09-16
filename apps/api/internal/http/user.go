package http

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	openapiTypes "github.com/oapi-codegen/runtime/types"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
	"github.com/ahdirmai/jg-smm/apps/api/internal/service"
)

// UserHandler is the team CRUD API (P6-11). List needs `read`; create/update/
// delete need `admin`, which in the MVP means OWNER.
type UserHandler struct {
	users *service.UserService
}

// NewUserHandler wires the service.
func NewUserHandler(users *service.UserService) *UserHandler {
	return &UserHandler{users: users}
}

// Register mounts the user routes. The mutating routes carry their own `admin`
// gate on top of the group's `read`.
func (h *UserHandler) Register(g *echo.Group) {
	g.GET("/users", h.list)
	g.POST("/users", h.create, RequirePermission(domain.PermAdmin))
	g.PUT("/users/:userId", h.update, RequirePermission(domain.PermAdmin))
	g.DELETE("/users/:userId", h.remove, RequirePermission(domain.PermAdmin))
}

func (h *UserHandler) list(c echo.Context) error {
	rows, err := h.users.List(c.Request().Context())
	if err != nil {
		return translateUserError(err)
	}
	items := make([]oapigen.User, 0, len(rows))
	for _, row := range rows {
		items = append(items, toUserDTO(row))
	}
	return c.JSON(http.StatusOK, oapigen.UserList{Users: items})
}

func (h *UserHandler) create(c echo.Context) error {
	var req oapigen.CreateUserRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	saved, err := h.users.Create(c.Request().Context(), service.UserInput{
		Email:    string(req.Email),
		Name:     req.Name,
		Password: req.Password,
		Role:     domain.Role(req.Role),
	})
	if err != nil {
		return translateUserError(err)
	}
	return c.JSON(http.StatusCreated, toUserDTO(saved))
}

func (h *UserHandler) update(c echo.Context) error {
	var req oapigen.UpdateUserRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	claims, ok := ClaimsFrom(c)
	if !ok {
		return domain.ErrUnauthorized
	}
	var role *domain.Role
	if req.Role != nil {
		r := domain.Role(*req.Role)
		role = &r
	}
	updated, err := h.users.Update(c.Request().Context(), c.Param("userId"),
		ptrStr(req.Name), role, claims.UserID)
	if err != nil {
		return translateUserError(err)
	}
	return c.JSON(http.StatusOK, toUserDTO(updated))
}

func (h *UserHandler) remove(c echo.Context) error {
	claims, ok := ClaimsFrom(c)
	if !ok {
		return domain.ErrUnauthorized
	}
	if err := h.users.Remove(c.Request().Context(), c.Param("userId"), claims.UserID); err != nil {
		return translateUserError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func toUserDTO(s service.UserSummary) oapigen.User {
	return oapigen.User{
		Id:    s.ID,
		Email: openapiTypes.Email(s.Email),
		Name:  s.Name,
		Role:  oapigen.Role(s.Role),
	}
}

func translateUserError(err error) error {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrConflict):
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	default:
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
}
