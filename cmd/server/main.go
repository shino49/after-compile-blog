package main

import (
	"github.com/example/after-compile-blog/internal/app"
	"io/fs"
	"log"
	"net/http"
	"os"
)

func main() {
	db := os.Getenv("DATABASE_PATH")
	if db == "" {
		db = "./data/blog.db"
	}
	if err := app.EnsureDataDir(db); err != nil {
		log.Fatal(err)
	}
	var web fs.FS
	if d := os.Getenv("WEB_DIST"); d != "" {
		web = os.DirFS(d)
	} else if _, e := os.Stat("web/dist"); e == nil {
		web = os.DirFS("web/dist")
	}
	a, e := app.Open(db, web)
	if e != nil {
		log.Fatal(e)
	}
	defer app.Close(a)
	log.Printf("After Compile listening on %s", app.ParseAddr())
	log.Fatal(http.ListenAndServe(app.ParseAddr(), a.Router()))
}
