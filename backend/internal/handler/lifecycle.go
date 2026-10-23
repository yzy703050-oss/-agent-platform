package handler

import (
	"agent-platform/backend/internal/middleware"
	"agent-platform/backend/internal/model"
	"github.com/gin-gonic/gin"
)

func (a *API) RegisterPage(c *gin.Context) {
	if a.Pages == nil {
		failure(c, model.ErrConflict)
		return
	}
	p, e := a.Pages.Register(c.Request.Context(), middleware.Scope(c))
	if e != nil {
		failure(c, e)
		return
	}
	c.JSON(201, p)
}
func (a *API) HeartbeatPage(c *gin.Context) {
	if e := a.Pages.Heartbeat(c.Request.Context(), middleware.Scope(c), c.Param("id"), c.GetHeader("X-Page-Token")); e != nil {
		failure(c, e)
		return
	}
	c.Status(204)
}
func (a *API) LeavePage(c *gin.Context) {
	if e := a.Pages.Leave(c.Request.Context(), middleware.Scope(c), c.Param("id"), c.GetHeader("X-Page-Token")); e != nil {
		failure(c, e)
		return
	}
	c.Status(204)
}
func (a *API) GetRun(c *gin.Context) {
	run, e := a.Manager.GetRun(c.Request.Context(), middleware.Scope(c), c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	c.JSON(200, run)
}
func (a *API) PauseRun(c *gin.Context) {
	run, e := a.Manager.Pause(c.Request.Context(), middleware.Scope(c), c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	c.JSON(200, run)
}
func (a *API) ResumeRun(c *gin.Context) {
	var in struct {
		PageID string `json:"page_id"`
	}
	if !bind(c, &in) {
		return
	}
	s := middleware.Scope(c)
	if e := a.Pages.Verify(c.Request.Context(), s, in.PageID, c.GetHeader("X-Page-Token")); e != nil {
		failure(c, e)
		return
	}
	s.PageID = in.PageID
	run, e := a.Manager.Resume(c.Request.Context(), s, c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	c.JSON(200, run)
}
func (a *API) CancelRun(c *gin.Context) {
	run, e := a.Manager.Cancel(c.Request.Context(), middleware.Scope(c), c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	c.JSON(200, run)
}
