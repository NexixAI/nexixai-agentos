package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebSearch_BasicQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("expected /search, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("format") != "json" {
			t.Fatalf("expected format=json, got %s", r.URL.Query().Get("format"))
		}
		if r.URL.Query().Get("q") != "golang testing" {
			t.Fatalf("expected q=golang testing, got %s", r.URL.Query().Get("q"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResponse{
			Results: []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
				Engine  string `json:"engine"`
			}{
				{Title: "Go Testing", URL: "https://go.dev/doc/tutorial/add-a-test", Content: "Tutorial on testing in Go", Engine: "google"},
				{Title: "Testing Package", URL: "https://pkg.go.dev/testing", Content: "Package testing provides support", Engine: "duckduckgo"},
			},
			NumberOfResults: 2,
		})
	}))
	defer srv.Close()

	// Override SEARXNG_URL for test
	t.Setenv("SEARXNG_URL", srv.URL)

	handler := makeWebSearchHandler()
	params, _ := json.Marshal(webSearchInput{Query: "golang testing"})
	result, err := handler(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, ok := result.(webSearchOutput)
	if !ok {
		t.Fatalf("expected webSearchOutput, got %T", result)
	}

	if out.NumberOfResults != 2 {
		t.Errorf("expected 2 results, got %d", out.NumberOfResults)
	}
	if out.Query != "golang testing" {
		t.Errorf("expected query echo, got %s", out.Query)
	}
	if out.Results[0].Title != "Go Testing" {
		t.Errorf("expected title 'Go Testing', got %s", out.Results[0].Title)
	}
	if out.Results[1].Engine != "duckduckgo" {
		t.Errorf("expected engine 'duckduckgo', got %s", out.Results[1].Engine)
	}
}

func TestWebSearch_MaxResultsRespected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := make([]struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Engine  string `json:"engine"`
		}, 10)
		for i := range results {
			results[i] = struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
				Engine  string `json:"engine"`
			}{Title: "Result", URL: "https://example.com", Content: "Content", Engine: "google"}
		}
		json.NewEncoder(w).Encode(searxngResponse{Results: results, NumberOfResults: 10})
	}))
	defer srv.Close()

	t.Setenv("SEARXNG_URL", srv.URL)

	handler := makeWebSearchHandler()
	maxResults := 3
	params, _ := json.Marshal(webSearchInput{Query: "test", MaxResults: &maxResults})
	result, err := handler(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := result.(webSearchOutput)
	if out.NumberOfResults != 3 {
		t.Errorf("expected 3 results (capped), got %d", out.NumberOfResults)
	}
}

func TestWebSearch_EmptyQuery(t *testing.T) {
	handler := makeWebSearchHandler()
	params, _ := json.Marshal(webSearchInput{Query: ""})
	_, err := handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestWebSearch_SearXNGUnreachable(t *testing.T) {
	t.Setenv("SEARXNG_URL", "http://127.0.0.1:1")

	handler := makeWebSearchHandler()
	params, _ := json.Marshal(webSearchInput{Query: "test"})
	_, err := handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unreachable SearXNG")
	}
}

func TestWebSearch_SearXNGNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("format not enabled"))
	}))
	defer srv.Close()

	t.Setenv("SEARXNG_URL", srv.URL)

	handler := makeWebSearchHandler()
	params, _ := json.Marshal(webSearchInput{Query: "test"})
	_, err := handler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestWebSearch_CategoriesPassedThrough(t *testing.T) {
	var gotCategories string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCategories = r.URL.Query().Get("categories")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(searxngResponse{})
	}))
	defer srv.Close()

	t.Setenv("SEARXNG_URL", srv.URL)

	handler := makeWebSearchHandler()
	params, _ := json.Marshal(webSearchInput{Query: "test", Categories: "science,it"})
	handler(context.Background(), params)

	if gotCategories != "science,it" {
		t.Errorf("expected categories 'science,it', got '%s'", gotCategories)
	}
}
