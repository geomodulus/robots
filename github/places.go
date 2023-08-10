package github

import (
	"context"
	"encoding/json"
	"fmt"

	gh "github.com/google/go-github/v53/github"

	"github.com/geomodulus/citygraph"
	"github.com/geomodulus/robots/prettier"
)

// ArticleCheckout contains the contents of an article read directly from Github.
type PlaceCheckout struct {
	Slug     string
	Place    *citygraph.Place
	BodyHTML string
}

func (a *App) FetchPlace(ctx context.Context, slug string) (*PlaceCheckout, error) {
	// Get the head commit of the main branch
	ref, _, err := a.Git.GetRef(ctx, a.Owner, a.Repo, "refs/heads/main")
	if err != nil {
		return nil, fmt.Errorf("error getting reference: %v", err)
	}
	branchCommitSHA := *ref.Object.SHA

	res := &PlaceCheckout{
		Slug: removeQuotes(slug),
	}

	// poi.json
	jsonPath := "active_places/" + slug + "/poi.json"
	file, _, _, err := a.Repositories.GetContents(ctx, a.Owner, a.Repo, jsonPath, &gh.RepositoryContentGetOptions{Ref: branchCommitSHA})
	if err != nil {
		return nil, fmt.Errorf("error getting file content: %v", err)
	}
	content, err := file.GetContent()
	if err != nil {
		return nil, fmt.Errorf("error decoding file content: %v", err)
	}
	place := &citygraph.Place{}
	if err := json.Unmarshal([]byte(content), &place); err != nil {
		return nil, fmt.Errorf("error unmarshaling place: %v", err)
	}
	res.Place = place

	htmlPath := "active_places/" + slug + "/body.html"
	htmlFile, _, _, err := a.Repositories.GetContents(ctx, a.Owner, a.Repo, htmlPath, &gh.RepositoryContentGetOptions{Ref: branchCommitSHA})
	if err != nil {
		return nil, fmt.Errorf("error getting file content: %v", err)
	}
	htmlContent, err := htmlFile.GetContent()
	if err != nil {
		return nil, fmt.Errorf("error decoding file content: %v", err)
	}
	res.BodyHTML = htmlContent

	return res, nil
}

func (a *App) CreateOrUpdatePlacePullRequest(ctx context.Context, slug string, opts ...PullRequestOption) (int, string, error) {
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

	if params.Place != nil {
		// articles.json
		jsonPath := "active_places/" + slug + "/poi.json"
		jsonFileContent, err := json.MarshalIndent(params.Place, "", "  ")
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
		htmlPath := "active_places/" + slug + "/body.html"
		prettyBody, err := prettier.Format(params.BodyHTML, htmlPath)
		if err != nil {
			return 0, "", fmt.Errorf("error formatting html: %v\n\noffending html:\n%s", err, params.BodyHTML)
		}
		htmlTreeEntry := &gh.TreeEntry{
			Path:    gh.String(htmlPath),
			Mode:    gh.String("100644"),
			Type:    gh.String("blob"),
			Content: gh.String(prettyBody),
		}
		treeEntries = append(treeEntries, htmlTreeEntry)
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
	}

	return activePR.GetNumber(), activePR.GetHTMLURL(), nil
}
