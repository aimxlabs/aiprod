package search

import (
	"github.com/garett/aiprod/internal/docs"
	"github.com/garett/aiprod/internal/email"
)

type Result struct {
	Type  string      `json:"type"` // "doc", "email", "file", "task"
	ID    string      `json:"id"`
	Title string      `json:"title"`
	Score float64     `json:"score,omitempty"`
	Data  interface{} `json:"data"`
}

type Service struct {
	DocsStore  *docs.Store
	EmailStore *email.Store
}

func NewService(docsStore *docs.Store, emailStore *email.Store) *Service {
	return &Service{DocsStore: docsStore, EmailStore: emailStore}
}

func (s *Service) Search(query string, scopes []string, limit int) ([]Result, error) {
	if limit <= 0 {
		limit = 20
	}

	var results []Result
	shouldSearch := func(scope string) bool {
		if len(scopes) == 0 {
			return true
		}
		for _, s := range scopes {
			if s == scope {
				return true
			}
		}
		return false
	}

	if shouldSearch("docs") && s.DocsStore != nil {
		docResults, err := s.DocsStore.Search(query, limit)
		if err == nil {
			for _, d := range docResults {
				results = append(results, Result{
					Type:  "doc",
					ID:    d.ID,
					Title: d.Title,
					Data:  d,
				})
			}
		}
	}

	if shouldSearch("email") && s.EmailStore != nil {
		emailResults, err := s.EmailStore.Search(query, limit)
		if err == nil {
			for _, m := range emailResults {
				results = append(results, Result{
					Type:  "email",
					ID:    m.ID,
					Title: m.Subject,
					Data:  m,
				})
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}
