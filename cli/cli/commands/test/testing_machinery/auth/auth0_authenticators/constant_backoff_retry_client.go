/*
 * Copyright (c) 2021 - present Kurtosis Technologies Inc.
 * All Rights Reserved.
 */

package auth0_authenticators

import (
	"github.com/hashicorp/go-retryablehttp"
	"net/http"
	"time"
)

const (
	maxRetries = 5
	timeBetweenRetries = 3 * time.Second
)

// Gets a retrying client with a constant time in between retries
func getConstantBackoffRetryClient() *http.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = maxRetries
	retryClient.Backoff = func(min, max time.Duration, attemptNum int, resp *http.Response) time.Duration {
		return timeBetweenRetries
	}
	// Set retryClient logger off, otherwise you get annoying logs every request. https://github.com/hashicorp/go-retryablehttp/issues/31
	retryClient.Logger = nil
	return retryClient.StandardClient()
}
