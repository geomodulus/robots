package github

import (
	"context"
	"fmt"
	"math"
	"time"

	gh "github.com/google/go-github/v53/github"

	"github.com/geomodulus/citygraph"
)

// NewClient returns a new GitHub client for the given installation ID.
type App struct {
	*gh.Client

	ID             int64
	InstallationID int64
	Owner          string
	Repo           string
}

// CreateGithubInstallationToken creates a new GitHub installation token.
func (a *App) CreateInstallationToken(ctx context.Context) (string, error) {
	token, _, err := a.Apps.CreateInstallationToken(ctx, a.InstallationID, nil)
	if err != nil {
		return "", fmt.Errorf("CreateInstallationToken: %v", err)
	}
	return token.GetToken(), nil
}

type Params struct {
	InArchive     bool
	Article       *citygraph.Article
	Place         *citygraph.Place
	BodyHTML      string
	ArticleJS     string
	Locations     string
	CommitMessage string
	PRTitle       string
	PRBody        string
	PRNum         int
	TeaserGeoJSON string
	TeaserJS      string
}

type Option func(*Params)

func InArchive(inArchive bool) Option {
	return func(params *Params) {
		params.InArchive = inArchive
	}
}

func WithArticle(article *citygraph.Article) Option {
	return func(params *Params) {
		params.Article = article
	}
}

func WithPlace(place *citygraph.Place) Option {
	return func(params *Params) {
		params.Place = place
	}
}

func WithBodyHTML(bodyHTML string) Option {
	return func(params *Params) {
		params.BodyHTML = bodyHTML
	}
}

func WithArticleJS(articleJS string) Option {
	return func(params *Params) {
		params.ArticleJS = articleJS
	}
}

func WithLocations(locations string) Option {
	return func(params *Params) {
		params.Locations = locations
	}
}

func WithCommitMessage(msg string) Option {
	return func(params *Params) {
		params.CommitMessage = msg
	}
}

func WithPRNum(prNum int) Option {
	return func(params *Params) {
		params.PRNum = prNum
	}
}

func WithPRTitle(prTitle string) Option {
	return func(params *Params) {
		params.PRTitle = prTitle
	}
}

func WithPRBody(prBody string) Option {
	return func(params *Params) {
		params.PRBody = prBody
	}
}

func WithTeaserGeoJSON(geojson string) Option {
	return func(params *Params) {
		params.TeaserGeoJSON = geojson
	}
}

func WithTeaserJS(js string) Option {
	return func(params *Params) {
		params.TeaserJS = js
	}
}

func (a *App) newBranchRef(ctx context.Context) (*gh.Reference, error) {
	// No PR exists, create one
	ref, _, err := a.Git.GetRef(ctx, a.Owner, a.Repo, "refs/heads/main")
	if err != nil {
		return nil, fmt.Errorf("error getting reference: %v", err)
	}
	baseCommitSHA := *ref.Object.SHA

	newBranch := "scottie-" + time.Now().Format("20060102-150405")

	// Create a new reference (branch) pointing to the latest commit hash
	newBranchRef, _, err := a.Git.CreateRef(ctx, a.Owner, a.Repo, &gh.Reference{
		Ref:    gh.String("refs/heads/" + newBranch),
		Object: &gh.GitObject{SHA: &baseCommitSHA},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating reference: %v", err)
	}
	return newBranchRef, nil
}

func (a *App) createPRWithRetry(ctx context.Context, newPR *gh.NewPullRequest, maxRetries int) (*gh.PullRequest, error) {
	baseDelay := float64(2) // base delay in seconds
	maxDelay := float64(30) // maximum delay in seconds

	for i := 0; i < maxRetries; i++ {
		pr, _, err := a.PullRequests.Create(ctx, a.Owner, a.Repo, newPR)
		if err != nil {
			githubError, ok := err.(*gh.ErrorResponse)
			if ok {
				for _, err := range githubError.Errors {
					if err.Code == "custom" && err.Message == "No commits between main and "+*newPR.Head {
						delay := math.Min(baseDelay*math.Pow(2, float64(i)), maxDelay) // calculate delay
						fmt.Printf("PR creation failed. Retrying after %.2f seconds...\n", delay)
						time.Sleep(time.Duration(delay) * time.Second) // wait before retrying
						break
					}
				}
			} else {
				return nil, err
			}
		} else {
			return pr, nil
		}
	}
	return nil, fmt.Errorf("unable to create PR after %d attempts", maxRetries)
}

func removeQuotes(s string) string {
	if len(s) < 2 {
		return s
	}

	first := s[0]
	last := s[len(s)-1]

	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return s[1 : len(s)-1]
	}

	return s
}
