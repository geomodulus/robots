package search

import (
	"context"
	"fmt"

	"github.com/nekomeowww/go-pinecone"
	"github.com/pkoukk/tiktoken-go"
	"github.com/sashabaranov/go-openai"
)

// Constants
const (
	pineconeAccountRegion = "us-west1-gcp-free"
	pineconeProjectName   = "8432451" // index created with default project name in pinecone
	pineconeIndexName     = "search"  // index created with default project name in pinecone
	topK                  = int64(3)  // set default topK value
)

type Vector struct {
	ID       string
	Values   []float32              `json:"values"`
	Metadata map[string]interface{} `json:"metadata"` // changed "any" to "interface{}"
}

type UpsertVectorsParams struct {
	Vectors   []*Vector `json:"vectors"`
	Namespace string    `json:"namespace"`
}

type UpsertVectorsResponse struct {
	UpsertedCount int `json:"upsertedCount"`
}

// Struct for search client that contains OpenAI and Pinecone clients
type Client struct {
	openAIClient        *openai.Client
	pineconeIndexClient *pinecone.IndexClient
}

// Create Client instance
func NewClient(openAIKey string, pineconeAPIKey string) (*Client, error) {

	// Create OpenAI client
	openAIClient := openai.NewClient(openAIKey)

	if openAIClient == nil {
		return nil, fmt.Errorf("failed to create OpenAI client")
	}

	// Create Pinecone client
	pineconeIndexClient, err := pinecone.NewIndexClient(
		pinecone.WithIndexName(pineconeIndexName),
		pinecone.WithAPIKey(pineconeAPIKey),
		pinecone.WithEnvironment(pineconeAccountRegion),
		pinecone.WithProjectName(pineconeProjectName),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Pinecone client: %v", err)
	}

	return &Client{
		openAIClient:        openAIClient,
		pineconeIndexClient: pineconeIndexClient,
	}, nil
}

// Helper function take query convert to embeddings OpenAI
func getEmbeddings(client *openai.Client, query string) ([]float32, error) {

	encoding := "cl100k_base" // sets the encoding model to use

	// Create a TikToken encoding instance
	tke, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		return nil, fmt.Errorf("getEncoding: %v", err)
	}

	// Tokenize the query using TikToen
	tokens := tke.Encode(query, nil, nil)

	println("Token for content generated: ", tokens)

	// Make sure we do not exceed the token limit
	if len(tokens) > 8191 {
		tokens = tokens[:8191]
	}

	// Embedding request
	req := openai.EmbeddingRequestTokens{
		Input: [][]int{tokens},
		Model: openai.AdaEmbeddingV2,
	}

	ctx := context.Background()

	// Generate embeddings
	resp, err := client.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(resp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	return resp.Data[0].Embedding, nil
}

// Data struct for query response
type QueryResponse struct {
	Matches   []*QueryVector `json:"matches"`
	Namespace string         `json:"namespace"`
}

// Matches are  slices of QueryVector
type QueryVector struct {
	Vector
	Score float32 `json:"score"`
}

type QueryParams struct {
	IncludeMetadata bool      `json:"includeMetadata"`
	Vector          []float32 `json:"vector"`
	TopK            int64     `json:"topK"`
}

// Helper function to search Pinecone index
func searchPinecone(pineconeClient *pinecone.IndexClient, embedding []float32, topK int64) (*pinecone.QueryResponse, error) {
	// Search Pinecone index
	ctx := context.Background()
	params := pinecone.QueryParams{
		Vector:          embedding,
		TopK:            topK,
		IncludeMetadata: true,
	}
	resp, err := pineconeClient.Query(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to search Pinecone index: %v", err)
	}

	return resp, nil
}

// Template for search results
type SearchResult struct {
	Name    string
	ID      string
	Path    string
	Slug    string
	Score   float32
	PubDate string
}

// RunQuery is a method of Client struct, that returns results using the SearchResult struct
func (s *Client) RunQuery(query string) ([]*SearchResult, error) {

	// Get embedding of user query from OpenAI
	embeddings, err := getEmbeddings(s.openAIClient, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get embeddings: %v", err)
	}

	// Search query embeddings in Pinecone index
	searchResults, err := searchPinecone(s.pineconeIndexClient, embeddings, topK)
	if err != nil {
		return nil, fmt.Errorf("failed to search Pinecone index: %v", err)
	}

	// Return the search results
	out := []*SearchResult{}

	baseURL := "https://www.torontoverse.com"

	for _, result := range searchResults.Matches {

		searchResult := &SearchResult{
			ID:    result.ID,
			Score: result.Score,
		}
		if result.Metadata["article_name"] != nil {
			searchResult.Name, _ = result.Metadata["article_name"].(string)
		}
		if result.Metadata["path"] != nil {
			path, _ := result.Metadata["path"].(string)
			// Prepend the base URL to the path
			searchResult.Path = baseURL + path
		}
		if result.Metadata["slug"] != nil { // Check if "slug" exists in the metadata
			searchResult.Slug, _ = result.Metadata["slug"].(string) // Add the slug to the SearchResult
		}
		// Check if "pub_date" exists in the metadata and add it to the SearchResult struct
		if result.Metadata["pub_date"] != nil {
			searchResult.PubDate, _ = result.Metadata["pub_date"].(string)
		}
		out = append(out, searchResult)
	}

	return out, nil
}
