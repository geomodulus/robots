package search

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"strings"

	"github.com/geomodulus/citygraph"
	"github.com/microcosm-cc/bluemonday"
	pinecone "github.com/nekomeowww/go-pinecone"
)

// Project note: Embedding model text-embeddings-ada-002 has 1536 dimensions
// Pinecone index must be set to same number of dimensions

// Helper function to strip html tags from article body
func StripHTML(s string) string {
	p := bluemonday.StripTagsPolicy()
	return p.Sanitize(s)
}

// New struct that embeds graph.Article and adds Body field
type ArticleWithBody struct {
	*citygraph.Article
	Body string
}

type EmbeddingsRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// Helper function to upsert embeddings into Pinecone
func storeEmbeddings(pineconeClient *pinecone.IndexClient, id string, embeddings []float32, metadata map[string]interface{}) error {
	ctx := context.Background()
	params := pinecone.UpsertVectorsParams{
		Vectors: []*pinecone.Vector{
			{
				ID:       id,         // Article ID from graph
				Values:   embeddings, // Embedding of article
				Metadata: metadata,   // Used to return human readable article name and path
			},
		},
	}
	fmt.Printf("Upserting vector with ID: %s, Metadata: %v\n", id, metadata)

	resp, err := pineconeClient.UpsertVectors(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to upsert vectors: %v", err)
	}
	fmt.Printf("%+v\n", resp)
	return nil
}

type FetchVectorsResponse struct {
	Vectors   map[string]*Vector `json:"vectors"`
	Namespace string             `json:"namespace"`
}

// / Helper function to fetch embeddings from Pinecone
func fetchEmbeddings(pineconeClient *pinecone.IndexClient, id string, article *citygraph.Article) ([]float32, map[string]interface{}, error) {
	ctx := context.Background()
	params := pinecone.FetchVectorsParams{
		IDs: []string{id},
	}

	resp, err := pineconeClient.FetchVectors(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch vector: %v", err)
	}

	// Extract the values of the first (and only) result
	vector, exists := resp.Vectors[id]
	if !exists {
		// The vector for this ID does not exist, return a nil slice and no error
		return nil, nil, nil
	}

	embeddings := vector.Values

	// Check if metadata fields "article_name" and "path" exist and match the expected values
	articleName, ok := vector.Metadata["article_name"].(string)
	if !ok || articleName != article.Name {
		// "article_name" field is missing or doesn't match the expected value
		return nil, nil, fmt.Errorf("metadata for ID %v does not have correct 'article_name'", id)
	}
	path, ok := vector.Metadata["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		// "path" field is missing or empty
		return nil, nil, fmt.Errorf("metadata for ID %v does not have 'path' or it is empty", id)
	}
	pubDate, ok := vector.Metadata["pub_date"].(string)
	if !ok || pubDate != article.PubDate {
		// "pub_date" field is missing or doesn't match the expected value
		return nil, nil, fmt.Errorf("metadata for ID %v does not have correct 'pub_date'", id)
	}
	articlePath, err := article.Path()
	if err != nil || strings.TrimSpace(articlePath) == "" {
		// Error generating path or the generated path is empty
		return nil, nil, fmt.Errorf("failed to generate path for ID %v or it is empty", id)
	}
	if path != articlePath {
		// Paths don't match
		return nil, nil, fmt.Errorf("metadata for ID %v does not have correct 'path'", id)
	}
	slug, ok := vector.Metadata["slug"].(string)
	if !ok || slug != article.Slug {
		// "slug" field is missing or doesn't match the expected value
		return nil, nil, fmt.Errorf("metadata for ID %v does not have correct 'slug'", id)
	}

	fmt.Printf("Metadata for vector ID %s: %v\n", id, vector.Metadata)

	// At the end, return the embeddings and metadata
	return embeddings, vector.Metadata, nil
}

// Processes articles from Torontoverse Corpus and Indexes them in Pinecone
// Generate is method on Client struct defined in search.go
func (s *Client) Generate(articles []*citygraph.Article) error {

	var liveArticleCount int

	for _, article := range articles {
		if article.PubDate == "" || !article.IsLive {
			continue
		}
		liveArticleCount++
		fmt.Printf("-- Processing article %d: %s\n", liveArticleCount, article.Name)

		// Try to fetch existing embedding from Pinecone
		existingEmbedding, metadata, err := fetchEmbeddings(s.pineconeIndexClient, article.ID, article)
		if err == nil && existingEmbedding != nil && metadata != nil {
			// If there's no error and we get an embedding, it means that the embedding already exists

		} else {
			// If the vector doesn't exist, we get an error or nil embeddings
			// So, proceed with creating and storing embeddings
			body, err := article.LoadBodyText()
			if err != nil {
				// Log the error and continue with the next article
				log.Printf("Failed to read body text for article %s: %v", article.Name, err)
				continue
			}
			// Strip HTML tags from article body
			body = StripHTML(body)

			// Create instance of ArticleWithBody
			awb := ArticleWithBody{
				Article: article,
				Body:    body,
			}

			// Tempalte for es
			tmpl, err := template.New("es").Parse(`headline: {{.Article.Name}} subhead:{{.Article.Description}} authors:{{.Article.Authors}} pub_date:{{.Article.PubDate}} body: {{.Body}}`)
			if err != nil {
				return err
			}

			// Get path of article
			path, err := article.Path()
			if err != nil {
				log.Printf("Failed to get path for article %s: %v", article.Name, err)
				continue
			}

			// Print path
			fmt.Println("Path for article:", path)

			// Metadata to include when upserting embeddings to Pinecone
			metadata := map[string]interface{}{
				"article_name": article.Name,
				"path":         path,
				"pub_date":     article.PubDate,
				"slug":         article.Slug,
			}

			// Create the es variable using the template, tml
			var esBuilder strings.Builder
			err = tmpl.Execute(&esBuilder, awb)
			if err != nil {
				log.Printf("Failed to execute template for article %s: %v", article.Name, err)
				continue
			}
			es := esBuilder.String()

			// Print es variable
			fmt.Println(es)

			// Call OpenAI API to create embeddings for article content
			embeddings, err := getEmbeddings(s.openAIClient, es)
			if err != nil {
				// Log the error and continue with the next article
				log.Printf("Failed to get embeddings for article %s: %v", article.Name, err)
				continue
			}

			// Store embeddings in Pinecone
			err = storeEmbeddings(s.pineconeIndexClient, article.ID, embeddings, metadata)
			if err != nil {
				// Log the error and continue with the next article
				log.Printf("Failed to store embeddings for article %s in Pinecone: %v", article.Name, err)
				continue
			}

			fmt.Println("-- Embeddings stored for article:", article.Name)
		}
	}

	return nil
}
