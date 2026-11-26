package cats

import (
	"context"
	"errors"
	"io"
	"net/http"
)

const MaxGIFBytes = 10 << 20

type Client struct {
	HTTP *http.Client
	URL  string
}

func (c Client) GIF(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/gif")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("cat provider unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxGIFBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxGIFBytes {
		return nil, errors.New("cat GIF exceeds size limit")
	}
	if len(body) < 6 || (string(body[:6]) != "GIF87a" && string(body[:6]) != "GIF89a") {
		return nil, errors.New("cat provider returned an invalid GIF")
	}
	return body, nil
}
