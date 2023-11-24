package github

import (
	"context"
	"encoding/json"
	"fmt"

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

func (a *App) CreateOrUpdateArticlePullRequest(ctx context.Context, slug string, opts ...Option) (int, string, error) {
	var (
		prBranchRef *gh.Reference
		activePR    *gh.PullRequest
		err         error
	)

	params := Params{
		PRBody: "This PR was created dynamically.",
	}
	for _, opt := range opts {
		opt(&params)
	}

	var maybeArchive string
	if params.InArchive {
		maybeArchive = "archive/"
	}

	articlePath := maybeArchive + "articles/" + slug

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

	treeEntries, err := treeEntriesFromParams(articlePath, params)
	if err != nil {
		return 0, "", fmt.Errorf("error creating tree entries: %w", err)
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

func (a *App) CreateArticleCommit(ctx context.Context, slug string, opts ...Option) (string, error) {
	params := Params{}
	for _, opt := range opts {
		opt(&params)
	}

	var maybeArchive string
	if params.InArchive {
		maybeArchive = "archive/"
	}

	articlePath := maybeArchive + "articles/" + slug

	// Step 1: Get the latest commit of the branch
	ref, _, err := a.Git.GetRef(ctx, a.Owner, a.Repo, "refs/heads/main")
	if err != nil {
		panic(err)
	}

	// Step 2: Create a tree with the new article
	treeEntries, err := treeEntriesFromParams(articlePath, params)
	if err != nil {
		return "", fmt.Errorf("error creating tree entries: %w", err)
	}

	// Step 3: Commit the changes.
	baseSHA := ref.GetObject().GetSHA()
	tree, _, err := a.Git.CreateTree(ctx, a.Owner, a.Repo, baseSHA, treeEntries)
	if err != nil {
		return "", fmt.Errorf("error creating tree: %v", err)
	}
	parentCommit, _, err := a.Git.GetCommit(ctx, a.Owner, a.Repo, baseSHA)
	if err != nil {
		return "", fmt.Errorf("error getting commit: %v", err)
	}
	commit, _, err := a.Git.CreateCommit(ctx, a.Owner, a.Repo, &gh.Commit{
		Message: gh.String(params.CommitMessage),
		Tree:    tree,
		Parents: []*gh.Commit{parentCommit},
	})
	if err != nil {
		return "", fmt.Errorf("error creating commit: %v", err)
	}

	return *commit.SHA, nil
}

func treeEntriesFromParams(path string, params Params) ([]*gh.TreeEntry, error) {
	treeEntries := []*gh.TreeEntry{}

	if params.Article != nil {
		entry, err := articleTreeEntry(path, params.Article)
		if err != nil {
			return nil, fmt.Errorf("error creating article tree entry: %w", err)
		}
		treeEntries = append(treeEntries, entry)
	}

	if params.BodyHTML != "" {
		// articles.html
		htmlTreeEntry, err := articleBodyHTML(path, params.BodyHTML)
		if err != nil {
			return nil, fmt.Errorf("error creating article body html tree entry: %w", err)
		}
		treeEntries = append(treeEntries, htmlTreeEntry)
	}

	if params.ArticleJS != "" {
		// articles.js
		jsTreeEntry, err := articleJS(path, params.ArticleJS)
		if err != nil {
			return nil, fmt.Errorf("error creating article js tree entry: %w", err)
		}
		treeEntries = append(treeEntries, jsTreeEntry)
	}

	if params.TeaserGeoJSON != "" {
		// teaser.geojson
		entry, err := articleTeaserGeoJSON(path, params.TeaserGeoJSON)
		if err != nil {
			return nil, fmt.Errorf("error creating article teaser geojson tree entry: %w", err)
		}
		treeEntries = append(treeEntries, entry)
	}

	if params.TeaserJS != "" {
		// teaser.js
		entry, err := articleTeaserJS(path, params.TeaserJS)
		if err != nil {
			return nil, fmt.Errorf("error creating article teaser js tree entry: %w", err)
		}
		treeEntries = append(treeEntries, entry)
	}

	if (params.Article != nil) && (params.Locations != "") {
		// locations.geojson
		if len(params.Article.GeoJSONDatasets) > 0 && params.Article.GeoJSONDatasets[0].Name == "locations" {
			entries, err := articleGeoJSONDatasets(path, params.Locations)
			if err != nil {
				return nil, fmt.Errorf("error creating article geojson datasets tree entries: %w", err)
			}
			treeEntries = append(treeEntries, entries...)
		}
	}

	return treeEntries, nil
}

func articleTreeEntry(path string, article *citygraph.Article) (*gh.TreeEntry, error) {
	// articles.json
	jsonPath := path + "/article.json"
	jsonFileContent, err := json.MarshalIndent(article, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("error marshaling json: %w", err)
	}
	prettyJSONFileContent, err := prettier.Format(string(jsonFileContent), jsonPath)
	if err != nil {
		return nil, fmt.Errorf("error formatting json: %w", err)
	}

	return &gh.TreeEntry{
		Path:    gh.String(jsonPath),
		Mode:    gh.String("100644"),
		Type:    gh.String("blob"),
		Content: gh.String(string(prettyJSONFileContent)),
	}, nil
}

func articleBodyHTML(path string, bodyHTML string) (*gh.TreeEntry, error) {
	htmlPath := path + "/article.html"
	prettyBody, err := prettier.Format(bodyHTML, htmlPath)
	if err != nil {
		return nil, fmt.Errorf("error formatting html: %v\n\noffending html:\n%s", err, bodyHTML)
	}
	return &gh.TreeEntry{
		Path:    gh.String(htmlPath),
		Mode:    gh.String("100644"),
		Type:    gh.String("blob"),
		Content: gh.String(prettyBody),
	}, nil
}

func articleJS(path string, js string) (*gh.TreeEntry, error) {
	jsPath := path + "/article.js"
	prettyJS, err := prettier.Format(js, jsPath)
	if err != nil {
		return nil, fmt.Errorf("error formatting javascript: %v\n\noffending javascript:\n%s", err, js)
	}
	return &gh.TreeEntry{
		Path:    gh.String(jsPath),
		Mode:    gh.String("100644"),
		Type:    gh.String("blob"),
		Content: gh.String(prettyJS),
	}, nil
}

func articleTeaserGeoJSON(path string, geoJSON string) (*gh.TreeEntry, error) {
	geoJSONPath := path + "/teaser.geojson"
	prettyGeoJSON, err := prettier.Format(geoJSON, geoJSONPath)
	if err != nil {
		return nil, fmt.Errorf("error formatting javascript: %v", err)
	}
	return &gh.TreeEntry{
		Path:    gh.String(geoJSONPath),
		Mode:    gh.String("100644"),
		Type:    gh.String("blob"),
		Content: gh.String(prettyGeoJSON),
	}, nil
}

func articleTeaserJS(path string, js string) (*gh.TreeEntry, error) {
	jsPath := path + "/teaser.js"
	prettyJS, err := prettier.Format(js, jsPath)
	if err != nil {
		return nil, fmt.Errorf("error formatting javascript: %v", err)
	}
	return &gh.TreeEntry{
		Path:    gh.String(jsPath),
		Mode:    gh.String("100644"),
		Type:    gh.String("blob"),
		Content: gh.String(prettyJS),
	}, nil
}

func articleGeoJSONDatasets(path string, locations string) ([]*gh.TreeEntry, error) {
	treeEntries := []*gh.TreeEntry{}

	locDatasetPath := path + "/locations.geojson"
	prettyGeoJSON, err := prettier.Format(locations, locDatasetPath)
	if err != nil {
		return nil, fmt.Errorf("error formatting javascript: %v", err)
	}
	locationsTreeEntry := &gh.TreeEntry{
		Path:    gh.String(locDatasetPath),
		Mode:    gh.String("100644"),
		Type:    gh.String("blob"),
		Content: gh.String(prettyGeoJSON),
	}
	treeEntries = append(treeEntries, locationsTreeEntry)

	locDatasetJSPath := path + "/locations.js"
	prettyLocJS, err := prettier.Format("console.debug('locations.js');", locDatasetJSPath)
	if err != nil {
		return nil, fmt.Errorf("error formatting javascript: %v", err)
	}
	locationsJSTreeEntry := &gh.TreeEntry{
		Path:    gh.String(locDatasetJSPath),
		Mode:    gh.String("100644"),
		Type:    gh.String("blob"),
		Content: gh.String(prettyLocJS),
	}
	treeEntries = append(treeEntries, locationsJSTreeEntry)

	return treeEntries, nil
}
