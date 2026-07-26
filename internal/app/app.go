package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var migrations embed.FS

type App struct {
	DB     *sql.DB
	Web    fs.FS
	Secure bool
}
type ctxKey string

const userKey ctxKey = "user"

type Post struct {
	ID             int64      `json:"id"`
	Title          string     `json:"title"`
	Slug           string     `json:"slug"`
	Summary        string     `json:"summary"`
	Content        string     `json:"contentMarkdown"`
	Status         string     `json:"status"`
	Category       string     `json:"category"`
	CoverURL       string     `json:"coverUrl"`
	Featured       bool       `json:"featured"`
	PublishedAt    *time.Time `json:"publishedAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	Tags           []string   `json:"tags"`
	ReadingMinutes int        `json:"readingMinutes"`
}
type postInput struct {
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Summary     string     `json:"summary"`
	Content     string     `json:"contentMarkdown"`
	Status      string     `json:"status"`
	Category    string     `json:"category"`
	CoverURL    string     `json:"coverUrl"`
	Featured    bool       `json:"featured"`
	PublishedAt *time.Time `json:"publishedAt"`
	Tags        []string   `json:"tags"`
}

func Open(dbPath string, web fs.FS) (*App, error) {
	db, e := sql.Open("sqlite", dbPath+"?_foreign_keys=on&_busy_timeout=5000")
	if e != nil {
		return nil, e
	}
	schema, _ := migrations.ReadFile("schema.sql")
	if _, e = db.Exec(string(schema)); e != nil {
		return nil, e
	}
	a := &App{DB: db, Web: web, Secure: os.Getenv("COOKIE_SECURE") == "true"}
	if e = a.seed(); e != nil {
		return nil, e
	}
	return a, nil
}
func (a *App) seed() error {
	var n int
	a.DB.QueryRow("SELECT count(*) FROM users").Scan(&n)
	if n == 0 {
		u, p := os.Getenv("ADMIN_USERNAME"), os.Getenv("ADMIN_PASSWORD")
		if u != "" && p != "" {
			if len(p) < 12 {
				return errors.New("ADMIN_PASSWORD must have at least 12 characters")
			}
			h, _ := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
			if _, e := a.DB.Exec("INSERT INTO users(username,password_hash) VALUES(?,?)", u, h); e != nil {
				return e
			}
		} else {
			log.Print("administrator not created: set ADMIN_USERNAME and ADMIN_PASSWORD")
		}
	}
	a.DB.QueryRow("SELECT count(*) FROM posts").Scan(&n)
	if n == 0 {
		_, e := a.DB.Exec(`INSERT INTO posts(title,slug,summary,content_markdown,status,category,featured,published_at) VALUES
('从可靠的服务边界开始','reliable-service-boundaries','关于超时、幂等与可观测性的工程笔记。','# 从可靠的服务边界开始\n\n系统的可靠性，始于清晰的边界。\n\n## 超时不是可选项\n\n每一次远程调用都应该有明确期限。','published','技术',1,CURRENT_TIMESTAMP),
('屏幕熄灭之后','after-the-screen-sleeps','夜晚散步时，重新理解慢下来的意义。','# 屏幕熄灭之后\n\n编译结束，生活才刚刚开始。','published','随笔',0,CURRENT_TIMESTAMP)`)
		return e
	}
	return nil
}
func (a *App) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]any{"status": "ok"}) })
	r.Route("/api", func(r chi.Router) {
		r.Get("/posts", a.publicPosts)
		r.Get("/posts/{slug}", a.publicPost)
		r.Get("/featured", a.featured)
		r.Get("/archive", a.archive)
		r.Get("/search", a.search)
		r.Post("/auth/login", a.login)
		r.Group(func(r chi.Router) {
			r.Use(a.auth)
			r.Get("/auth/me", a.me)
			r.Post("/auth/logout", a.logout)
			r.Route("/admin", func(r chi.Router) {
				r.Use(a.sameOrigin)
				r.Get("/dashboard", a.dashboard)
				r.Get("/posts", a.adminPosts)
				r.Post("/posts", a.createPost)
				r.Put("/posts/{id}", a.updatePost)
				r.Delete("/posts/{id}", a.deletePost)
			})
		})
	})
	r.Handle("/*", a.spa())
	return r
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func apiErr(w http.ResponseWriter, status int, code, msg string, fields map[string]string) {
	jsonOut(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg, "fields": fields}})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Username, Password string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		apiErr(w, 400, "invalid_json", "请求格式无效", nil)
		return
	}
	var id int64
	var hash string
	e := a.DB.QueryRow("SELECT id,password_hash FROM users WHERE username=?", in.Username).Scan(&id, &hash)
	if e != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		time.Sleep(100 * time.Millisecond)
		apiErr(w, 401, "invalid_credentials", "用户名或密码错误", nil)
		return
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	exp := time.Now().Add(7 * 24 * time.Hour)
	a.DB.Exec("DELETE FROM sessions WHERE expires_at<CURRENT_TIMESTAMP")
	a.DB.Exec("INSERT INTO sessions(token_hash,user_id,expires_at) VALUES(?,?,?)", hex.EncodeToString(sum[:]), id, exp)
	http.SetCookie(w, &http.Cookie{Name: "after_session", Value: token, Path: "/", HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode, Expires: exp})
	jsonOut(w, 200, map[string]any{"authenticated": true, "username": in.Username})
}
func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie("after_session")
		if e != nil {
			apiErr(w, 401, "unauthorized", "请先登录", nil)
			return
		}
		sum := sha256.Sum256([]byte(c.Value))
		var id int64
		var name string
		e = a.DB.QueryRow(`SELECT u.id,u.username FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>CURRENT_TIMESTAMP`, hex.EncodeToString(sum[:])).Scan(&id, &name)
		if e != nil {
			apiErr(w, 401, "session_expired", "登录已失效，请重新登录", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, map[string]any{"id": id, "username": name})))
	})
}
func (a *App) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" || r.Method == "HEAD" {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			u, e := url.Parse(origin)
			if e != nil || !strings.EqualFold(u.Host, r.Host) {
				apiErr(w, 403, "csrf_rejected", "请求来源无效", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) me(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, r.Context().Value(userKey)) }
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("after_session"); e == nil {
		sum := sha256.Sum256([]byte(c.Value))
		a.DB.Exec("DELETE FROM sessions WHERE token_hash=?", hex.EncodeToString(sum[:]))
	}
	http.SetCookie(w, &http.Cookie{Name: "after_session", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode})
	jsonOut(w, 200, map[string]bool{"ok": true})
}
func scanPost(s interface{ Scan(...any) error }) (Post, error) {
	var p Post
	var featured int
	var pub sql.NullTime
	e := s.Scan(&p.ID, &p.Title, &p.Slug, &p.Summary, &p.Content, &p.Status, &p.Category, &p.CoverURL, &featured, &pub, &p.CreatedAt, &p.UpdatedAt)
	p.Featured = featured == 1
	if pub.Valid {
		p.PublishedAt = &pub.Time
	}
	p.ReadingMinutes = max(1, len([]rune(p.Content))/500)
	p.Tags = []string{}
	return p, e
}

const selectPost = `SELECT id,title,slug,summary,content_markdown,status,category,COALESCE(cover_url,''),featured,published_at,created_at,updated_at FROM posts`

func (a *App) tags(p *Post) {
	rows, _ := a.DB.Query(`SELECT t.name FROM tags t JOIN post_tags pt ON pt.tag_id=t.id WHERE pt.post_id=? ORDER BY t.name`, p.ID)
	if rows == nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		rows.Scan(&t)
		p.Tags = append(p.Tags, t)
	}
}
func (a *App) list(q string, args ...any) ([]Post, error) {
	rows, e := a.DB.Query(selectPost+q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		p, e := scanPost(rows)
		if e != nil {
			return nil, e
		}
		a.tags(&p)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (a *App) publicPosts(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit := 10
	where := " WHERE status='published'"
	args := []any{}
	if v := r.URL.Query().Get("category"); v != "" {
		where += " AND category=?"
		args = append(args, v)
	}
	if v := r.URL.Query().Get("tag"); v != "" {
		where += " AND id IN(SELECT post_id FROM post_tags pt JOIN tags t ON t.id=pt.tag_id WHERE t.slug=?)"
		args = append(args, v)
	}
	var total int
	a.DB.QueryRow("SELECT count(*) FROM posts"+where, args...).Scan(&total)
	args = append(args, limit, (page-1)*limit)
	items, e := a.list(where+" ORDER BY published_at DESC LIMIT ? OFFSET ?", args...)
	if e != nil {
		apiErr(w, 500, "internal_error", "读取文章失败", nil)
		return
	}
	jsonOut(w, 200, map[string]any{"items": items, "page": page, "pageSize": limit, "total": total})
}
func (a *App) publicPost(w http.ResponseWriter, r *http.Request) {
	p, e := scanPost(a.DB.QueryRow(selectPost+" WHERE slug=? AND status='published'", chi.URLParam(r, "slug")))
	if e == sql.ErrNoRows {
		apiErr(w, 404, "not_found", "文章不存在", nil)
		return
	}
	if e != nil {
		apiErr(w, 500, "internal_error", "读取文章失败", nil)
		return
	}
	a.tags(&p)
	jsonOut(w, 200, p)
}
func (a *App) featured(w http.ResponseWriter, r *http.Request) {
	x, _ := a.list(" WHERE status='published' AND featured=1 ORDER BY published_at DESC LIMIT 6")
	jsonOut(w, 200, x)
}
func (a *App) search(w http.ResponseWriter, r *http.Request) {
	q := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	x, _ := a.list(" WHERE status='published' AND (title LIKE ? OR summary LIKE ? OR content_markdown LIKE ?) ORDER BY published_at DESC LIMIT 50", q, q, q)
	jsonOut(w, 200, x)
}
func (a *App) archive(w http.ResponseWriter, r *http.Request) {
	x, _ := a.list(" WHERE status='published' ORDER BY published_at DESC")
	jsonOut(w, 200, x)
}
func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	var total, pub, draft int
	a.DB.QueryRow("SELECT count(*),sum(status='published'),sum(status='draft') FROM posts").Scan(&total, &pub, &draft)
	rows, _ := a.DB.Query("SELECT category,count(*) FROM posts GROUP BY category")
	cats := map[string]int{}
	for rows.Next() {
		var c string
		var n int
		rows.Scan(&c, &n)
		cats[c] = n
	}
	rows.Close()
	recent, _ := a.list(" ORDER BY updated_at DESC LIMIT 5")
	jsonOut(w, 200, map[string]any{"total": total, "published": pub, "drafts": draft, "categories": cats, "recent": recent})
}
func (a *App) adminPosts(w http.ResponseWriter, r *http.Request) {
	where := " WHERE 1=1"
	args := []any{}
	if s := r.URL.Query().Get("status"); s != "" {
		where += " AND status=?"
		args = append(args, s)
	}
	if q := r.URL.Query().Get("q"); q != "" {
		where += " AND title LIKE ?"
		args = append(args, "%"+q+"%")
	}
	x, _ := a.list(where+" ORDER BY updated_at DESC", args...)
	jsonOut(w, 200, x)
}
func validate(in postInput) map[string]string {
	f := map[string]string{}
	if strings.TrimSpace(in.Title) == "" {
		f["title"] = "不能为空"
	}
	if strings.TrimSpace(in.Slug) == "" {
		f["slug"] = "不能为空"
	} else {
		for _, c := range in.Slug {
			if !(c == '-' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				f["slug"] = "仅支持小写字母、数字和连字符"
				break
			}
		}
	}
	if strings.TrimSpace(in.Content) == "" {
		f["contentMarkdown"] = "不能为空"
	}
	if in.Status != "draft" && in.Status != "published" {
		f["status"] = "必须是 draft 或 published"
	}
	if strings.TrimSpace(in.Category) == "" {
		f["category"] = "不能为空"
	}
	return f
}
func (a *App) saveTags(tx *sql.Tx, id int64, tags []string) error {
	tx.Exec("DELETE FROM post_tags WHERE post_id=?", id)
	for _, name := range tags {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		slug := slugify(name)
		_, e := tx.Exec("INSERT OR IGNORE INTO tags(name,slug) VALUES(?,?)", name, slug)
		if e != nil {
			return e
		}
		_, e = tx.Exec("INSERT OR IGNORE INTO post_tags(post_id,tag_id) SELECT ?,id FROM tags WHERE name=?", id, name)
		if e != nil {
			return e
		}
	}
	return nil
}
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), "-")
	return s
}
func (a *App) createPost(w http.ResponseWriter, r *http.Request) {
	var in postInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		apiErr(w, 400, "invalid_json", "请求格式无效", nil)
		return
	}
	if f := validate(in); len(f) > 0 {
		apiErr(w, 422, "validation_error", "请检查文章字段", f)
		return
	}
	tx, _ := a.DB.Begin()
	pub := in.PublishedAt
	if in.Status == "published" && pub == nil {
		t := time.Now()
		pub = &t
	}
	res, e := tx.Exec(`INSERT INTO posts(title,slug,summary,content_markdown,status,category,cover_url,featured,published_at) VALUES(?,?,?,?,?,?,?,?,?)`, in.Title, in.Slug, in.Summary, in.Content, in.Status, in.Category, in.CoverURL, in.Featured, pub)
	if e != nil {
		tx.Rollback()
		if strings.Contains(e.Error(), "UNIQUE") {
			apiErr(w, 409, "slug_conflict", "slug 已被使用", map[string]string{"slug": "已存在"})
			return
		}
		apiErr(w, 500, "internal_error", "保存失败", nil)
		return
	}
	id, _ := res.LastInsertId()
	a.saveTags(tx, id, in.Tags)
	tx.Commit()
	p, _ := scanPost(a.DB.QueryRow(selectPost+" WHERE id=?", id))
	a.tags(&p)
	jsonOut(w, 201, p)
}
func (a *App) updatePost(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if e != nil {
		apiErr(w, 404, "not_found", "文章不存在", nil)
		return
	}
	var in postInput
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		apiErr(w, 400, "invalid_json", "请求格式无效", nil)
		return
	}
	if f := validate(in); len(f) > 0 {
		apiErr(w, 422, "validation_error", "请检查文章字段", f)
		return
	}
	tx, _ := a.DB.Begin()
	pub := in.PublishedAt
	if in.Status == "published" && pub == nil {
		t := time.Now()
		pub = &t
	}
	res, e := tx.Exec(`UPDATE posts SET title=?,slug=?,summary=?,content_markdown=?,status=?,category=?,cover_url=?,featured=?,published_at=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, in.Title, in.Slug, in.Summary, in.Content, in.Status, in.Category, in.CoverURL, in.Featured, pub, id)
	if e != nil {
		tx.Rollback()
		if strings.Contains(e.Error(), "UNIQUE") {
			apiErr(w, 409, "slug_conflict", "slug 已被使用", map[string]string{"slug": "已存在"})
			return
		}
		apiErr(w, 500, "internal_error", "保存失败", nil)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		tx.Rollback()
		apiErr(w, 404, "not_found", "文章不存在", nil)
		return
	}
	a.saveTags(tx, id, in.Tags)
	tx.Commit()
	p, _ := scanPost(a.DB.QueryRow(selectPost+" WHERE id=?", id))
	a.tags(&p)
	jsonOut(w, 200, p)
}
func (a *App) deletePost(w http.ResponseWriter, r *http.Request) {
	res, _ := a.DB.Exec("DELETE FROM posts WHERE id=?", chi.URLParam(r, "id"))
	n, _ := res.RowsAffected()
	if n == 0 {
		apiErr(w, 404, "not_found", "文章不存在", nil)
		return
	}
	w.WriteHeader(204)
}
func (a *App) spa() http.Handler {
	if a.Web == nil {
		return http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." {
			name = "index.html"
		}
		if _, e := fs.Stat(a.Web, name); e != nil {
			name = "index.html"
		}
		http.ServeFileFS(w, r, a.Web, name)
	})
}
func ParseAddr() string {
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return ":8080"
}
func EnsureDataDir(p string) error {
	d := path.Dir(p)
	if d != "." {
		return os.MkdirAll(d, 0755)
	}
	return nil
}
func Close(a *App) {
	if e := a.DB.Close(); e != nil {
		fmt.Println(e)
	}
}
