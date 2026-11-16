package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/exports"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
)

func (s *Server) transferRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/imports", s.listImports)
		r.Get("/api/v1/imports/{importID}", s.getImport)
		r.Get("/api/v1/import-unmatched", s.listUnmatched)
		r.Get("/api/v1/export/library.jsonl", s.exportNative)
		r.Get("/api/v1/export/library.csv", s.exportCSV)
		r.Get("/api/v1/export/letterboxd.csv", s.exportLetterboxd)
		r.Get("/api/v1/export-files", s.exportFiles)
		r.Get("/api/v1/export-files/{fileID}", s.exportFile)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/imports", s.startImport)
		r.Post("/api/v1/imports/{importID}/cancel", s.cancelImport)
		r.Post("/api/v1/imports/{importID}/rollback", s.rollbackImport)
		r.Post("/api/v1/import-unmatched/{unmatchedID}/resolve", s.resolveUnmatched)
		r.Post("/api/v1/import-unmatched/{unmatchedID}/ignore", s.ignoreUnmatched)
	})
}

func (s *Server) importsReady(w http.ResponseWriter, r *http.Request) bool {
	if s.imports.Pool == nil || s.imports.Catalog == nil {
		s.problem(w, r, fault.Unavailable)
		return false
	}
	return true
}

// startImport accepts multipart form data with "source" and "file", or JSON
// {"source":"anilist","username":"..."} for sources read from a public profile.
func (s *Server) startImport(w http.ResponseWriter, r *http.Request) {
	if !s.importsReady(w, r) || !s.accountLimit(w, r, "import", 10) {
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		s.problem(w, r, &fault.Error{Status: 415, Code: "unsupported_media_type", Message: "Send multipart form data with a file, or JSON."})
		return
	}
	var source, name, username string
	var upload []byte
	switch mediaType {
	case "application/json":
		var in struct {
			Source   string `json:"source"`
			Username string `json:"username"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			s.problem(w, r, fault.Invalid("Send a JSON object with source and username."))
			return
		}
		source, username = in.Source, in.Username
	case "multipart/form-data":
		r.Body = http.MaxBytesReader(w, r.Body, 65<<20)
		mr, err := r.MultipartReader()
		if err != nil || params["boundary"] == "" {
			s.problem(w, r, fault.Invalid("Malformed multipart body."))
			return
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				s.problem(w, r, fault.Invalid("The upload was interrupted or is larger than 64 MiB."))
				return
			}
			switch part.FormName() {
			case "source":
				b, _ := io.ReadAll(io.LimitReader(part, 64))
				source = strings.TrimSpace(string(b))
			case "username":
				b, _ := io.ReadAll(io.LimitReader(part, 128))
				username = strings.TrimSpace(string(b))
			case "file":
				name = part.FileName()
				if upload, err = io.ReadAll(io.LimitReader(part, 64<<20+1)); err != nil {
					s.problem(w, r, fault.Invalid("The upload was interrupted or is larger than 64 MiB."))
					return
				}
			}
		}
	default:
		s.problem(w, r, &fault.Error{Status: 415, Code: "unsupported_media_type", Message: "Send multipart form data with a file, or JSON."})
		return
	}
	run, err := s.imports.Start(r.Context(), actor(r), source, name, upload, username)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 202, run)
}

func (s *Server) listImports(w http.ResponseWriter, r *http.Request) {
	if !s.importsReady(w, r) {
		return
	}
	runs, err := s.imports.List(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": runs})
}

func (s *Server) getImport(w http.ResponseWriter, r *http.Request) {
	if !s.importsReady(w, r) {
		return
	}
	run, err := s.imports.Get(r.Context(), actor(r), chi.URLParam(r, "importID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, run)
}

func (s *Server) cancelImport(w http.ResponseWriter, r *http.Request) {
	if !s.importsReady(w, r) {
		return
	}
	run, err := s.imports.Cancel(r.Context(), actor(r), chi.URLParam(r, "importID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, run)
}

func attachment(w http.ResponseWriter, contentType, name string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
}

// exportNative writes one JSON object per line in the format imports.ParseNative reads.
func (s *Server) exportNative(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, "jsonl", "library")
}
func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) { s.stream(w, r, "csv", "library") }

// exportLetterboxd writes watched movies in Letterboxd's import format.
func (s *Server) exportLetterboxd(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, "letterboxd", "letterboxd")
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, format, prefix string) {
	if !s.accountLimit(w, r, "export", 10) {
		return
	}
	f := exports.Formats[format]
	attachment(w, f.ContentType, prefix+"-"+time.Now().UTC().Format("2006-01-02")+"."+f.Ext)
	sw, buf := newExport(w)
	_, err := exports.Write(r.Context(), s.library, actor(r), format, buf)
	s.finishStream(sw, r, buf, err)
}

// sentWriter remembers whether any byte reached the client.
type sentWriter struct {
	http.ResponseWriter
	sent bool
}

func (w *sentWriter) Write(b []byte) (int, error) {
	w.sent = true
	return w.ResponseWriter.Write(b)
}

func newExport(w http.ResponseWriter) (*sentWriter, *bufio.Writer) {
	sw := &sentWriter{ResponseWriter: w}
	return sw, bufio.NewWriterSize(sw, 64<<10)
}

// finishStream flushes a successful export. A failure before any byte was sent becomes a
// normal problem response; after the stream started it can only be logged and cut short.
func (s *Server) finishStream(sw *sentWriter, r *http.Request, buf *bufio.Writer, err error) {
	if err == nil {
		err = buf.Flush()
	}
	if err == nil {
		return
	}
	if !sw.sent {
		sw.Header().Del("Content-Disposition")
		s.problem(sw.ResponseWriter, r, err)
		return
	}
	s.logger.Error("export interrupted", "route", r.URL.Path)
	panic(http.ErrAbortHandler)
}

func (s *Server) listUnmatched(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	q := r.URL.Query()
	out, err := s.imports.Unmatched(r.Context(), actor(r), q.Get("run_id"), q.Get("status"), q.Get("cursor"), limit)
	s.reply(w, r, out, err)
}

func (s *Server) resolveUnmatched(w http.ResponseWriter, r *http.Request) {
	if !s.importsReady(w, r) {
		return
	}
	id, err := pathID(r, "unmatchedID")
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[struct {
		CatalogID string `json:"catalog_id"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	catalogID, err := identity.ParseID(in.CatalogID)
	if err != nil {
		s.problem(w, r, fault.Invalid("Choose a catalog title."))
		return
	}
	out, err := s.imports.Resolve(r.Context(), actor(r), id, catalogID)
	s.reply(w, r, out, err)
}

func (s *Server) ignoreUnmatched(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "unmatchedID")
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.imports.Ignore(r.Context(), actor(r), id)
	s.reply(w, r, out, err)
}

func (s *Server) rollbackImport(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "importID")
	if !identity.ValidUUID(id) {
		s.problem(w, r, fault.NotFound)
		return
	}
	run, entries, plays, err := s.imports.Rollback(r.Context(), actor(r), id)
	s.reply(w, r, map[string]any{"import": run, "entries_removed": entries, "plays_removed": plays}, err)
}

func (s *Server) exportStore() exports.Store {
	return exports.Store{Pool: s.imports.Pool, Library: s.library}
}

func (s *Server) exportFiles(w http.ResponseWriter, r *http.Request) {
	out, err := s.exportStore().Files(r.Context(), actor(r))
	s.reply(w, r, map[string]any{"items": out}, err)
}

func (s *Server) exportFile(w http.ResponseWriter, r *http.Request) {
	f, body, err := s.exportStore().Open(r.Context(), actor(r), chi.URLParam(r, "fileID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	defer body.Close()
	format := exports.Formats[f.Format]
	attachment(w, format.ContentType, "pastime-"+f.Format+"-"+f.CreatedAt.UTC().Format("2006-01-02")+"."+format.Ext)
	_, _ = io.Copy(w, body)
}

func (s *Server) exportRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/me/export-schedule", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.exportStore().Schedule(r.Context(), actor(r))
			if err == nil && out == nil {
				respond(w, 200, map[string]any{"schedule": nil})
				return
			}
			s.reply(w, r, map[string]any{"schedule": out}, err)
		})
		r.Put("/api/v1/me/export-schedule", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[exports.Schedule](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.exportStore().SaveSchedule(r.Context(), actor(r), in)
			s.reply(w, r, map[string]any{"schedule": out}, err)
		})
		r.Delete("/api/v1/me/export-schedule", func(w http.ResponseWriter, r *http.Request) {
			if err := s.exportStore().DeleteSchedule(r.Context(), actor(r)); err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r.Post("/api/v1/export-files", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				Format string `json:"format"`
			}](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			if !s.accountLimit(w, r, "export", 10) {
				return
			}
			out, err := s.exportStore().SnapshotNow(r.Context(), actor(r), in.Format)
			s.replyStatus(w, r, 201, out, err)
		})
		r.Delete("/api/v1/export-files/{fileID}", func(w http.ResponseWriter, r *http.Request) {
			if err := s.exportStore().DeleteFile(r.Context(), actor(r), chi.URLParam(r, "fileID")); err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
}
