package server

import (
	"github.com/gin-gonic/gin"

	"invoice/internal/billing"
)

func registerTitles(g *gin.RouterGroup, s *billing.Service, admin bool) {
	path := "/titles"
	if admin {
		path = "/users/:id/titles"
	}
	g.GET(path, wrap(func(c *gin.Context) (any, error) {
		user := actor(c).ID
		var e error
		if admin {
			user, e = pathID(c)
			if e != nil {
				return nil, e
			}
		}
		f, e := filter(c, "keyword")
		if e != nil {
			return nil, e
		}
		return s.Titles(c.Request.Context(), user, f, admin)
	}))
	g.POST(path, wrap(func(c *gin.Context) (any, error) {
		user := actor(c).ID
		var e error
		if admin {
			user, e = pathID(c)
			if e != nil {
				return nil, e
			}
		}
		var in billing.TitleInput
		if e = body(c, &in); e != nil {
			return nil, e
		}
		return s.SaveTitle(c.Request.Context(), write(c), user, 0, in, false)
	}))
	if !admin {
		g.PUT("/titles/:id", wrap(func(c *gin.Context) (any, error) {
			id, e := pathID(c)
			if e != nil {
				return nil, e
			}
			var in billing.TitleInput
			if e = body(c, &in); e != nil {
				return nil, e
			}
			return s.SaveTitle(c.Request.Context(), write(c), actor(c).ID, id, in, false)
		}))
		g.DELETE("/titles/:id", wrap(func(c *gin.Context) (any, error) {
			id, e := pathID(c)
			if e != nil {
				return nil, e
			}
			if c.Request.ContentLength != 0 {
				return nil, bad()
			}
			return s.SaveTitle(c.Request.Context(), write(c), actor(c).ID, id, billing.TitleInput{}, true)
		}))
	}
}
func registerApplications(g *gin.RouterGroup, s *billing.Service, admin bool) {
	orders := "/orders/eligible"
	if admin {
		orders = "/users/:id/orders/eligible"
	}
	g.GET(orders, wrap(func(c *gin.Context) (any, error) {
		user := actor(c).ID
		var e error
		if admin {
			user, e = pathID(c)
			if e != nil {
				return nil, e
			}
		}
		f, e := filter(c, "keyword", "topup_ids")
		if e != nil {
			return nil, e
		}
		return s.Orders(c.Request.Context(), user, f, admin)
	}))
	g.GET("/applications", wrap(func(c *gin.Context) (any, error) {
		allowed := []string{"keyword", "status"}
		if admin {
			allowed = append(allowed, "user_id")
		}
		f, e := filter(c, allowed...)
		if e != nil {
			return nil, e
		}
		return s.Applications(c.Request.Context(), actor(c), f, admin)
	}))
	g.POST("/applications", wrap(func(c *gin.Context) (any, error) {
		var in billing.CreateInput
		if e := body(c, &in); e != nil {
			return nil, e
		}
		if !admin && in.UserID != "" {
			return nil, bad()
		}
		return s.Create(c.Request.Context(), write(c), in)
	}))
	g.GET("/applications/:id", wrap(func(c *gin.Context) (any, error) {
		id, e := pathID(c)
		if e != nil {
			return nil, e
		}
		if _, e = filter(c); e != nil {
			return nil, e
		}
		return s.Detail(c.Request.Context(), actor(c), id, admin)
	}))
	transition := "cancel"
	if admin {
		transition = "reject"
	}
	g.POST("/applications/:id/"+transition, wrap(func(c *gin.Context) (any, error) {
		id, e := pathID(c)
		if e != nil {
			return nil, e
		}
		remark := ""
		if admin {
			var in struct {
				Remark string `json:"admin_remark"`
			}
			if e = body(c, &in); e != nil {
				return nil, e
			}
			remark = in.Remark
		} else {
			var empty struct{}
			if e = body(c, &empty); e != nil {
				return nil, e
			}
		}
		return s.Transition(c.Request.Context(), write(c), id, remark)
	}))
}
