package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"invoice/internal/billing"
	"invoice/internal/fault"
	"invoice/internal/files"
)

func registerFiles(api, admin *gin.RouterGroup, s *billing.Service, fs *files.Store) {
	for _, g := range []*gin.RouterGroup{api, admin} {
		isAdmin := g == admin
		g.GET("/applications/:id/file", func(c *gin.Context) {
			id, e := pathID(c)
			if e != nil {
				reply(c, nil, e)
				return
			}
			if _, e = filter(c); e != nil {
				reply(c, nil, e)
				return
			}
			d, e := s.Download(c.Request.Context(), actor(c), id, isAdmin)
			if e != nil {
				reply(c, nil, e)
				return
			}
			if d.MIME != "application/pdf" && d.MIME != "image/jpeg" && d.MIME != "image/png" {
				reply(c, nil, fault.New(410, "FILE_UNAVAILABLE", "文件暂不可用，请联系管理员"))
				return
			}
			f, e := fs.Open(d.Path)
			if e != nil {
				reply(c, nil, fault.New(410, "FILE_UNAVAILABLE", "文件暂不可用，请联系管理员"))
				return
			}
			defer f.Close()
			info, e := f.Stat()
			if e != nil || info.Size() == 0 {
				reply(c, nil, fault.New(410, "FILE_UNAVAILABLE", "文件暂不可用，请联系管理员"))
				return
			}
			c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Name}))
			c.Header("Cache-Control", "no-store")
			c.DataFromReader(200, info.Size(), d.MIME, f, nil)
		})
	}
	admin.POST("/applications/:id/complete", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			reply(c, nil, e)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 11*1024*1024)
		reader, e := c.Request.MultipartReader()
		if e != nil {
			reply(c, nil, fault.New(415, "UNSUPPORTED_MEDIA_TYPE", "需要 multipart/form-data"))
			return
		}
		var prepared files.Prepared
		remark := ""
		count := 0
		seenRemark := false
		keep := false
		defer func() {
			if prepared.Path != "" && !keep {
				if e := fs.Remove(prepared.Path); e != nil {
					slog.Warn("unreferenced file cleanup deferred", "request_id", c.GetString("request_id"))
				}
			}
		}()
		for {
			part, e := reader.NextPart()
			if errors.Is(e, io.EOF) {
				break
			}
			if e != nil {
				reply(c, nil, fault.New(413, "FILE_TOO_LARGE", "上传内容过大或不完整"))
				return
			}
			switch part.FormName() {
			case "file":
				count++
				if count != 1 || part.FileName() == "" {
					part.Close()
					reply(c, nil, bad())
					return
				}
				prepared, e = fs.Prepare(part.FileName(), part.Header.Get("Content-Type"), part)
			case "admin_remark":
				if seenRemark || part.FileName() != "" {
					part.Close()
					reply(c, nil, bad())
					return
				}
				seenRemark = true
				var raw []byte
				raw, e = io.ReadAll(io.LimitReader(part, 2049))
				if e == nil {
					remark, e = billing.Text(string(raw), 512, false)
				}
			default:
				part.Close()
				reply(c, nil, bad())
				return
			}
			part.Close()
			if e != nil {
				var f *fault.Error
				if !errors.As(e, &f) {
					e = fault.New(503, "STORAGE_UNAVAILABLE", "附件存储暂不可用")
				}
				reply(c, nil, e)
				return
			}
		}
		if count != 1 {
			reply(c, nil, bad())
			return
		}
		result, e := s.Complete(c.Request.Context(), write(c), id, prepared, remark)
		if e != nil {
			// An uncertain COMMIT may still become durable. Retain conservatively;
			// reconciliation reports these objects without deleting them automatically.
			var f *fault.Error
			if !errors.As(e, &f) || f.Status >= 500 {
				keep = true
			}
			reply(c, nil, e)
			return
		}
		// A same-key replay returns the first file; remove only this unused preparation.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var used bool
		if e = s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM invoice_applications WHERE invoice_file_path=$1)", prepared.Path).Scan(&used); e != nil {
			keep = true
		} else {
			keep = used
		}
		reply(c, result, nil)
	})
}
