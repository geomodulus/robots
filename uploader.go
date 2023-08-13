package robots

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	"cloud.google.com/go/storage"
)

const bucketName = "media.geomodul.us"

type Uploader struct {
	client     *storage.Client
	slackToken string
	prefix     string
}

func NewUploader(ctx context.Context, slackToken string, prefix string) (*Uploader, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	return &Uploader{
		client:     client,
		slackToken: slackToken,
		prefix:     prefix,
	}, nil
}

func (u *Uploader) Upload(ctx context.Context, slug, downloadURL string) (string, error) {
	var objectKey string
	if slug == "" {
		objectKey = fmt.Sprintf("img/%s", path.Base(downloadURL))
	} else {
		parsedURL, err := url.Parse(downloadURL)
		if err != nil {
			return "", fmt.Errorf("url.Parse: %v", err)
		}
		objectKey = fmt.Sprintf("%s/%s/%s", u.prefix, slug, path.Base(parsedURL.Path))
	}

	// Create a new HTTP request to download the file.
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("http.NewRequest: %v", err)
	}

	// Add the authorization header to the request.
	req.Header.Add("Authorization", "Bearer "+u.slackToken)

	// Do the request.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http.DefaultClient.Do: %v", err)
	}
	defer resp.Body.Close()

	// Write the file to the specified GCS bucket.
	wc := u.client.Bucket(bucketName).Object(objectKey).NewWriter(ctx)
	if _, err = io.Copy(wc, resp.Body); err != nil {
		return "", fmt.Errorf("io.Copy: %v", err)
	}
	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("Writer.Close: %v", err)
	}
	fmt.Printf("Blob %s uploaded.\n", wc.Attrs().Name)
	return fmt.Sprintf("https://%s/%s", bucketName, objectKey), nil
}
