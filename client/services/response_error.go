package services

import (
	"net/http"

	"github.com/calypr/syfon/client/apierror"
)

func apiResponseError(resp *http.Response, body []byte) *apierror.APIError {
	return apierror.FromResponse(resp, body)
}
