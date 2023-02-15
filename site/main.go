package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/carterjs/webtools/assets"
	"github.com/carterjs/webtools/cache"
	"github.com/carterjs/webtools/graphql"
	"github.com/carterjs/webtools/templates"
)

var directusURL = os.Getenv("DIRECTUS_URL")

// Types from directus
type (
	Configuration struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Image       Image  `json:"image"`
		Disclaimer  string `json:"disclaimer"`
	}
	Candidate struct {
		Slug     string        `json:"slug"`
		Name     string        `json:"name"`
		ShortBio string        `json:"short_bio"`
		Bio      template.HTML `json:"bio"`
		Image    Image         `json:"image"`
	}
	Image struct {
		ID string `json:"id"`
	}
	Priority struct {
		Slug    string        `json:"slug"`
		Title   string        `json:"title"`
		Content template.HTML `json:"content"`
	}
	News struct {
		Title       string  `json:"title"`
		ContentType string  `json:"content_type"`
		Source      string  `json:"source"`
		Link        string  `json:"link"`
		Article     Article `json:"article"`
	}
	Article struct {
		Slug        string        `json:"slug"`
		Title       string        `json:"title"`
		Description string        `json:"description"`
		Content     template.HTML `json:"content"`
	}
)

var assetServer = assets.NewServer("/static", "./static")

var defaultTTL = time.Minute * 10
var tmpl = template.Must(templates.ParseDir("./templates", template.FuncMap{
	"getAssetURL": func(id string) string {
		return fmt.Sprintf(directusURL + "/assets/" + id)
	},
	"getFirstName": func(name string) string {
		return strings.Split(name, " ")[0]
	},
	"disclaimer": cache.Func(defaultTTL, func() (string, error) {
		data, err := query[struct {
			Configuration `json:"configuration"`
		}](`
			{
				configuration {
					disclaimer
				}
			}
		`, nil)

		if err != nil {
			return "", err
		}

		return data.Disclaimer, nil
	}),
	"configuration": cache.Func(defaultTTL, func() (Configuration, error) {
		data, err := query[struct {
			Configuration `json:"configuration"`
		}](`
			{
				configuration {
					title
					description
					image {
						id
					}
				}
			}
		`, nil)
		if err != nil {
			return Configuration{}, err
		}

		return data.Configuration, nil
	}),
	"candidates": cache.Func(defaultTTL, func() ([]Candidate, error) {
		data, err := query[struct {
			Candidates []Candidate `json:"candidates"`
		}](`
			{
				candidates {
					slug
					name
					bio
					short_bio
					image {
						id
					}
				}
			}
		`, nil)
		if err != nil {
			return nil, err
		}

		return data.Candidates, nil
	}),
	"priorities": cache.Func(defaultTTL, func() ([]Priority, error) {
		data, err := query[struct {
			Priorities []Priority `json:"priorities"`
		}](`
			{
				priorities {
					slug
					title
					content
				}
			}
		`, nil)
		if err != nil {
			return nil, err
		}

		return data.Priorities, nil
	}),
	"news": cache.Func(defaultTTL, func() ([]News, error) {
		data, err := query[struct {
			News []News `json:"news"`
		}](`
			{
				news {
					content_type
					article {
						slug
					}
					title
					link
					source
				}
			}
		`, nil)
		if err != nil {
			return nil, err
		}

		return data.News, nil
	}),
	"getVersionedPath": assetServer.GetVersionedPath,
}))

func main() {
	if directusURL == "" {
		directusURL = "https://admin.osterbergschmalzle.com"
	}

	// serve assets
	http.Handle("/static/", assetServer)

	handleArticleDetailPage()

	handlePage("/candidates", "candidates")
	handlePage("/priorities", "priorities")
	handlePage("/news", "news")
	handlePage("/", "home")

	// Start the server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Println("Starting server on port", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("Could not start server: %v", err)
	}
}

func handleArticleDetailPage() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered from", r)
		}
	}()

	type articleGetter func() (*Article, error)
	articles := map[string]articleGetter{}
	http.HandleFunc("/articles/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
			return
		}

		slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/articles/"), "/")

		var getter articleGetter
		if existing, ok := articles[slug]; ok {
			getter = existing
		} else {
			getter = cache.Func(defaultTTL, func() (*Article, error) {
				data, err := query[struct {
					Articles []Article `json:"articles"`
				}](`
					query getArticleBySlug($slug: String) {
						articles(filter:{
							slug: {
								_eq: $slug
							}
						}) {
							title
							description
							content
						}
					}
				`, map[string]any{
					"slug": slug,
				})
				if err != nil {
					return nil, err
				}

				if len(data.Articles) == 0 {
					return nil, nil
				}

				return &data.Articles[0], nil
			})
			articles[slug] = getter
		}

		article, err := getter()
		if err != nil {
			render500(w)
			return
		}

		if article == nil {
			render404(w)
			return
		}

		renderPage(w, "article", article)
	})
}

// handlePage registers a handler to render the template or 404
func handlePage(path string, templateName string) {
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
			return
		}

		if r.URL.Path != path {
			render404(w)
			return
		}

		renderPage(w, templateName, r)
	})
}

func render404(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	renderPage(w, "404", nil)
}

func render500(w http.ResponseWriter) {
	w.WriteHeader(http.StatusInternalServerError)
	renderPage(w, "500", nil)
}

// renderPage executes the template or returns a 500
func renderPage(w http.ResponseWriter, templateName string, data any) {
	err := tmpl.ExecuteTemplate(w, templateName, data)
	if err != nil {
		log.Println("failed to parse template:", err)
	}
}

// query executes a graphql query against directus
func query[T any](q string, variables map[string]any) (*T, error) {
	return graphql.Query[T](directusURL+"/graphql", q, variables)
}
