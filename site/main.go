package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"
	"github.com/tdewolff/minify/v2/svg"
)

var directusURL = os.Getenv("DIRECTUS_URL")

// Types from directus
type (
	Configuration struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Image       Image  `json:"image"`
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
		Title  string `json:"title"`
		Source string `json:"source"`
		Link   string `json:"link"`
	}
)

// tmpl contains all parsed templates from the templates folder
// it does not look inside of folders
var tmpl = template.New("").Funcs(template.FuncMap{
	"getAssetURL": func(id string) string {
		return fmt.Sprintf(directusURL + "/assets/" + id)
	},
	"getFirstName": func(name string) string {
		return strings.Split(name, " ")[0]
	},
})

var minifier *minify.M

func init() {
	minifier = minify.New()
	minifier.AddFunc("text/css", css.Minify)
	minifier.AddFunc("image/svg+xml", svg.Minify)
	minifier.AddFunc("text/html", html.Minify)
	minifier.AddFuncRegexp(regexp.MustCompile("^(application|text)/(x-)?(java|ecma)script$"), js.Minify)

	// get and minify templates
	err := filepath.Walk("./templates", func(path string, info fs.FileInfo, err error) error {
		if strings.Contains(path, ".html") {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			mb, err := minifier.Bytes("text/html", b)
			if err != nil {
				return err
			}

			_, err = tmpl.Parse(string(mb))
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		log.Fatalf("failed to parse templates: %v", err)
	}

}

func main() {
	if directusURL == "" {
		directusURL = "https://admin.osterbergschmalzle.com"
	}

	// Serve static assets
	fileServer := http.StripPrefix("/static", http.FileServer(http.Dir("./static")))
	fileServerWithCaching := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		fileServer.ServeHTTP(w, r)
	})

	http.Handle("/static/css/", minifier.Middleware(fileServerWithCaching))
	http.Handle("/static/js/", minifier.Middleware(fileServerWithCaching))
	http.Handle("/static/", fileServerWithCaching)

	handlePage[struct {
		Candidates []Candidate `json:"candidates"`
	}]("/candidates", "candidates", `
		{
			candidates {
				slug
				name
				bio
				image {
					id
				}
			}
		}
	`)

	handlePage[struct {
		Priorities []Priority `json:"priorities"`
	}]("/priorities", "priorities", `
		{
			priorities {
				slug
				title
				content
			}
		}
	`)

	handlePage[struct {
		News []News `json:"news"`
	}]("/news", "news", `
		{
			news {
				title
				link
				source
			}
		}
	`)

	handlePage[struct {
		Configuration Configuration `json:"configuration"`
		Candidates    []Candidate   `json:"candidates"`
		Priorities    []Priority    `json:"priorities"`
		News          []News        `json:"news"`
	}]("/", "home", `
		{
			configuration {
				title
				description
				image {
					id
				}
			}
			candidates {
				slug
				name
				short_bio
				image {
					id
				}
			}
			priorities {
				slug
				title
			}
			news {
				title
				link
				source
			}
		}
	`)

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

// handlePage registers a handler to render the template or 404
func handlePage[T any](path string, templateName string, q string) {
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
			return
		}

		var data *T
		if r.URL.Path != path {
			// Not an exact match, 404
			w.WriteHeader(http.StatusNotFound)
			renderPage(w, "404", nil)
			return
		} else if q != "" {
			var err error
			data, err = query[T](q)
			if err != nil {
				log.Printf("Internal server error: %v", err)
				http.Error(w, "failed to fetch data", http.StatusInternalServerError)
				return
			}
		}

		renderPage(w, templateName, data)
	})
}

// renderPage executes the template or returns a 500
func renderPage(w http.ResponseWriter, templateName string, data any) {
	err := tmpl.ExecuteTemplate(w, templateName, data)
	if err != nil {
		http.Error(w, "failed to parse template", http.StatusInternalServerError)
	}
}

// query executes a graphql query against directus
func query[T any](q string) (*T, error) {
	url := directusURL + "/graphql"
	bodyBytes, err := json.Marshal(map[string]any{
		"query": q,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}

	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errBody string
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("error response from api: %v", resp.Status)
	}

	var responseBody struct {
		Data *T `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&responseBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return responseBody.Data, nil
}
