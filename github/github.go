package github

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	gh "github.com/google/go-github/v53/github"
	"github.com/paulmach/go.geojson"

	"github.com/geomodulus/citygraph"
	"github.com/geomodulus/robots/prettier"
)

// ArticleCheckout contains the contents of an article read directly from Github.
type ArticleCheckout struct {
	Slug               string
	Article            *citygraph.Article
	BodyHTML           string
	JavascriptFunction string
	LocationsGeoJSON   *geojson.FeatureCollection
}

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

func (a *App) FetchArticle(ctx context.Context, slug string) (*ArticleCheckout, error) {
	// Get the head commit of the main branch
	ref, _, err := a.Git.GetRef(ctx, a.Owner, a.Repo, "refs/heads/main")
	if err != nil {
		return nil, fmt.Errorf("error getting reference: %v", err)
	}
	branchCommitSHA := *ref.Object.SHA

	res := &ArticleCheckout{
		Slug: removeQuotes(slug),
	}

	// articles.json
	jsonPath := "articles/" + slug + "/article.json"
	file, _, _, err := a.Repositories.GetContents(ctx, a.Owner, a.Repo, jsonPath, &gh.RepositoryContentGetOptions{Ref: branchCommitSHA})
	if err != nil {
		return nil, fmt.Errorf("error getting file content: %v", err)
	}
	content, err := file.GetContent()
	if err != nil {
		return nil, fmt.Errorf("error decoding file content: %v", err)
	}
	article := &citygraph.Article{}
	if err := json.Unmarshal([]byte(content), &article); err != nil {
		return nil, fmt.Errorf("error unmarshaling article: %v", err)
	}
	// Update article via new method? here
	res.Article = article

	htmlPath := "articles/" + slug + "/article.html"
	htmlFile, _, _, err := a.Repositories.GetContents(ctx, a.Owner, a.Repo, htmlPath, &gh.RepositoryContentGetOptions{Ref: branchCommitSHA})
	if err != nil {
		return nil, fmt.Errorf("error getting file content: %v", err)
	}
	htmlContent, err := htmlFile.GetContent()
	if err != nil {
		return nil, fmt.Errorf("error decoding file content: %v", err)
	}
	res.BodyHTML = htmlContent

	jsPath := "articles/" + slug + "/article.js"
	jsFile, _, _, err := a.Repositories.GetContents(ctx, a.Owner, a.Repo, jsPath, &gh.RepositoryContentGetOptions{Ref: branchCommitSHA})
	if err != nil {
		return nil, fmt.Errorf("error getting file content: %v", err)
	}
	jsContent, err := jsFile.GetContent()
	if err != nil {
		return nil, fmt.Errorf("error decoding file content: %v", err)
	}
	res.JavascriptFunction = jsContent

	for _, dataset := range article.GeoJSONDatasets {
		if dataset.Name != "locations" {
			continue
		}
		locationsGeoJSONPath := "articles/" + slug + "/locations.geojson"
		locationsGeoJSONFile, _, _, err := a.Repositories.GetContents(ctx, a.Owner, a.Repo, locationsGeoJSONPath, &gh.RepositoryContentGetOptions{Ref: branchCommitSHA})
		if err != nil {
			return nil, fmt.Errorf("error getting file content: %v", err)
		}
		locationsGeoJSONContent, err := locationsGeoJSONFile.GetContent()
		if err != nil {
			return nil, fmt.Errorf("error decoding file content: %v", err)
		}
		locationsGeoJSON, err := geojson.UnmarshalFeatureCollection([]byte(locationsGeoJSONContent))
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling locations geojson: %v", err)
		}
		res.LocationsGeoJSON = locationsGeoJSON
	}
	return res, nil
}

type PullRequestParams struct {
	Article       *citygraph.Article
	BodyHTML      string
	ArticleJS     string
	Locations     string
	PRTitle       string
	PRBody        string
	PRNum         int
	TeaserGeoJSON string
	TeaserJS      string
}

type PullRequestOption func(*PullRequestParams)

func WithArticle(article *citygraph.Article) PullRequestOption {
	return func(params *PullRequestParams) {
		params.Article = article
	}
}

func WithBodyHTML(bodyHTML string) PullRequestOption {
	return func(params *PullRequestParams) {
		params.BodyHTML = bodyHTML
	}
}

func WithArticleJS(articleJS string) PullRequestOption {
	return func(params *PullRequestParams) {
		params.ArticleJS = articleJS
	}
}

func WithLocations(locations string) PullRequestOption {
	return func(params *PullRequestParams) {
		params.Locations = locations
	}
}

func WithPRNum(prNum int) PullRequestOption {
	return func(params *PullRequestParams) {
		params.PRNum = prNum
	}
}

func WithPRTitle(prTitle string) PullRequestOption {
	return func(params *PullRequestParams) {
		params.PRTitle = prTitle
	}
}

func WithPRBody(prBody string) PullRequestOption {
	return func(params *PullRequestParams) {
		params.PRBody = prBody
	}
}

func WithTeaserGeoJSON(geojson string) PullRequestOption {
	return func(params *PullRequestParams) {
		params.TeaserGeoJSON = geojson
	}
}

func WithTeaserJS(js string) PullRequestOption {
	return func(params *PullRequestParams) {
		params.TeaserJS = js
	}
}

func (a *App) CreateOrUpdatePullRequest(ctx context.Context, slug string, opts ...PullRequestOption) (int, string, error) {
	var (
		prBranchRef *gh.Reference
		activePR    *gh.PullRequest
		err         error
	)

	params := PullRequestParams{
		PRBody: "This PR was created dynamically.",
	}
	for _, opt := range opts {
		opt(&params)
	}

	if params.PRNum == 0 {
		// No PR exists, create one
		prBranchRef, err = a.newBranchRef(ctx)
		if err != nil {
			return 0, "", fmt.Errorf("error creating new branch: %v", err)
		}
	} else {
		// PR exists, check if it's been merged
		pr, _, err := a.PullRequests.Get(ctx, a.Owner, a.Repo, params.PRNum)
		if err != nil {
			return 0, "", fmt.Errorf("error getting PR: %v", err)
		}
		if *pr.State == "closed" {
			// Prior PR has been closed so, create a new one.
			prBranchRef, err = a.newBranchRef(ctx)
			if err != nil {
				return 0, "", fmt.Errorf("error creating new branch: %v", err)
			}
		} else {
			// PR still active, needs to be updated.
			prBranchRef, _, err = a.Git.GetRef(ctx, a.Owner, a.Repo, "refs/heads/"+pr.GetHead().GetRef())
			if err != nil {
				return 0, "", err
			}
			activePR = pr
		}
	}

	treeEntries := []*gh.TreeEntry{}

	if params.Article != nil {
		// articles.json
		jsonPath := "articles/" + slug + "/article.json"
		jsonFileContent, err := json.MarshalIndent(params.Article, "", "  ")
		if err != nil {
			return 0, "", fmt.Errorf("error marshaling json: %v", err)
		}
		prettyJSONFileContent, err := prettier.Format(string(jsonFileContent), jsonPath)
		if err != nil {
			return 0, "", fmt.Errorf("error formatting json: %v", err)
		}
		jsonTreeEntry := &gh.TreeEntry{
			Path:    gh.String(jsonPath),
			Mode:    gh.String("100644"),
			Type:    gh.String("blob"),
			Content: gh.String(string(prettyJSONFileContent)),
		}
		treeEntries = append(treeEntries, jsonTreeEntry)
	}

	if params.BodyHTML != "" {
		// articles.html
		htmlPath := "articles/" + slug + "/article.html"
		prettyBody, err := prettier.Format(params.BodyHTML, htmlPath)
		if err != nil {
			return 0, "", fmt.Errorf("error formatting html: %v\n\noffending html:\n%s", err, params.BodyHTML)
		}
		//	fmt.Println("saving", article.Slug, "to", htmlPath)
		//	fmt.Println(body)
		htmlTreeEntry := &gh.TreeEntry{
			Path:    gh.String(htmlPath),
			Mode:    gh.String("100644"),
			Type:    gh.String("blob"),
			Content: gh.String(prettyBody),
		}
		treeEntries = append(treeEntries, htmlTreeEntry)
	}

	if params.ArticleJS != "" {
		// articles.js
		jsPath := "articles/" + slug + "/article.js"
		prettyJS, err := prettier.Format(params.ArticleJS, jsPath)
		if err != nil {
			return 0, "", fmt.Errorf("error formatting javascript: %v\n\noffending JS:\n%s", err, params.ArticleJS)
		}
		jsTreeEntry := &gh.TreeEntry{
			Path:    gh.String(jsPath),
			Mode:    gh.String("100644"),
			Type:    gh.String("blob"),
			Content: gh.String(prettyJS),
		}
		treeEntries = append(treeEntries, jsTreeEntry)
	}

	if params.TeaserGeoJSON != "" {
		teaserGeoJSONPath := "articles/" + slug + "/teaser.geojson"
		prettyGeoJSON, err := prettier.Format(params.TeaserGeoJSON, teaserGeoJSONPath)
		if err != nil {
			return 0, "", fmt.Errorf("error formatting javascript: %v", err)
		}
		teaserGeoJSONTreeEntry := &gh.TreeEntry{
			Path:    gh.String(teaserGeoJSONPath),
			Mode:    gh.String("100644"),
			Type:    gh.String("blob"),
			Content: gh.String(prettyGeoJSON),
		}
		treeEntries = append(treeEntries, teaserGeoJSONTreeEntry)
	}

	if params.TeaserJS != "" {
		teaserJSPath := "articles/" + slug + "/teaser.js"
		prettyJS, err := prettier.Format(params.TeaserJS, teaserJSPath)
		if err != nil {
			return 0, "", fmt.Errorf("error formatting javascript: %v", err)
		}
		teaserJSTreeEntry := &gh.TreeEntry{
			Path:    gh.String(teaserJSPath),
			Mode:    gh.String("100644"),
			Type:    gh.String("blob"),
			Content: gh.String(prettyJS),
		}
		treeEntries = append(treeEntries, teaserJSTreeEntry)
	}

	// locations.geojson
	if (params.Article != nil) && (params.Locations != "") {
		if len(params.Article.GeoJSONDatasets) > 0 && params.Article.GeoJSONDatasets[0].Name == "locations" {
			locDatasetPath := "articles/" + slug + "/locations.geojson"
			prettyGeoJSON, err := prettier.Format(params.Locations, locDatasetPath)
			if err != nil {
				return 0, "", fmt.Errorf("error formatting javascript: %v", err)
			}
			locationsTreeEntry := &gh.TreeEntry{
				Path:    gh.String(locDatasetPath),
				Mode:    gh.String("100644"),
				Type:    gh.String("blob"),
				Content: gh.String(prettyGeoJSON),
			}
			treeEntries = append(treeEntries, locationsTreeEntry)

			locDatasetJSPath := "articles/" + slug + "/locations.js"
			prettyLocJS, err := prettier.Format("console.debug('locations.js');", locDatasetJSPath)
			if err != nil {
				return 0, "", fmt.Errorf("error formatting javascript: %v", err)
			}
			locationsJSTreeEntry := &gh.TreeEntry{
				Path:    gh.String(locDatasetJSPath),
				Mode:    gh.String("100644"),
				Type:    gh.String("blob"),
				Content: gh.String(prettyLocJS),
			}
			treeEntries = append(treeEntries, locationsJSTreeEntry)
		}
	}

	// Commit the changes.
	baseSHA := prBranchRef.GetObject().GetSHA()
	tree, _, err := a.Git.CreateTree(ctx, a.Owner, a.Repo, baseSHA, treeEntries)
	if err != nil {
		return 0, "", fmt.Errorf("error creating tree: %v", err)
	}
	parentCommit, _, err := a.Git.GetCommit(ctx, a.Owner, a.Repo, baseSHA)
	if err != nil {
		return 0, "", fmt.Errorf("error getting commit: %v", err)
	}
	commit, _, err := a.Git.CreateCommit(ctx, a.Owner, a.Repo, &gh.Commit{
		Message: gh.String(params.PRTitle),
		Tree:    tree,
		Parents: []*gh.Commit{parentCommit},
	})
	if err != nil {
		return 0, "", fmt.Errorf("error creating commit: %v", err)
	}

	// Add commit to the new branch.
	prBranchRef.Object.SHA = commit.SHA

	_, _, err = a.Git.UpdateRef(ctx, a.Owner, a.Repo, prBranchRef, false)
	if err != nil {
		return 0, "", fmt.Errorf("error updating reference: %v", err)
	}

	if activePR == nil {
		// Create a pull request
		newPR := &gh.NewPullRequest{
			Title:               gh.String(params.PRTitle),
			Head:                gh.String(prBranchRef.GetRef()),
			Base:                gh.String("main"),
			Body:                gh.String(params.PRBody),
			MaintainerCanModify: gh.Bool(true),
		}

		activePR, err = a.createPRWithRetry(ctx, newPR, 10)
		if err != nil {
			return 0, "", fmt.Errorf("error creating PR: %v", err)
		}

		// Add a reviewer to the pull request
		_, _, err = a.PullRequests.RequestReviewers(ctx, a.Owner, a.Repo, activePR.GetNumber(), gh.ReviewersRequest{
			Reviewers: []string{"chrisdinn"},
		})
		if err != nil {
			return 0, "", fmt.Errorf("error requesting reviewers: %v", err)
		}
	}

	return activePR.GetNumber(), activePR.GetHTMLURL(), nil
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
