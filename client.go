// Package aci is a Cisco ACI REST client library for Go.
package aci

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

const DefaultMaxRetries int = 3
const DefaultBackoffMinDelay int = 4
const DefaultBackoffMaxDelay int = 60
const DefaultBackoffDelayFactor float64 = 3

// Client is an HTTP ACI client.
// Use aci.NewClient to initiate a client.
// This will ensure proper cookie handling and processing of modifiers.
type Client struct {
	// HttpClient is the *http.Client used for API requests.
	HttpClient *http.Client
	// Url is the APIC IP or hostname, e.g. https://10.0.0.1:80 (port is optional).
	Url string
	// LastRefresh is the timestamp of the last token refresh interval.
	LastRefresh time.Time
	// Token is the current authentication token.
	Token string
	// Usr is the APIC username.
	Usr string
	// Pwd is the APIC password.
	Pwd string
	// Insecure determines if insecure https connections are allowed.
	Insecure bool
	// Maximum number of retries.
	MaxRetries int
	// Minimum delay between two retries.
	BackoffMinDelay int
	// Maximum delay between two retries.
	BackoffMaxDelay int
	// Backoff delay factor.
	BackoffDelayFactor float64
	// Authentication mutex
	AuthenticationMutex *sync.Mutex
	// Enable debug logging
	Logging bool
}

// NewClient creates a new ACI HTTP client.
// Pass modifiers in to modify the behavior of the client, e.g.
//
//	client, _ := NewClient("https://1.1.1.1", "user", "password", RequestTimeout(120))
func NewClient(url, usr, pwd string, mods ...func(*Client)) (Client, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	cookieJar, _ := cookiejar.New(nil)
	httpClient := http.Client{
		Timeout:   60 * time.Second,
		Transport: tr,
		Jar:       cookieJar,
	}

	client := Client{
		HttpClient:          &httpClient,
		Url:                 url,
		Usr:                 usr,
		Pwd:                 pwd,
		MaxRetries:          DefaultMaxRetries,
		BackoffMinDelay:     DefaultBackoffMinDelay,
		BackoffMaxDelay:     DefaultBackoffMaxDelay,
		BackoffDelayFactor:  DefaultBackoffDelayFactor,
		AuthenticationMutex: &sync.Mutex{},
		Logging:             false,
	}

	for _, mod := range mods {
		mod(&client)
	}
	return client, nil
}

// Insecure determines if insecure https connections are allowed. Default value is true.
func Insecure(x bool) func(*Client) {
	return func(client *Client) {
		client.HttpClient.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = x
	}
}

// RequestTimeout modifies the HTTP request timeout from the default of 60 seconds.
func RequestTimeout(x time.Duration) func(*Client) {
	return func(client *Client) {
		client.HttpClient.Timeout = x * time.Second
	}
}

// MaxRetries modifies the maximum number of retries from the default of 3.
func MaxRetries(x int) func(*Client) {
	return func(client *Client) {
		client.MaxRetries = x
	}
}

// BackoffMinDelay modifies the minimum delay between two retries from the default of 4.
func BackoffMinDelay(x int) func(*Client) {
	return func(client *Client) {
		client.BackoffMinDelay = x
	}
}

// BackoffMaxDelay modifies the maximum delay between two retries from the default of 60.
func BackoffMaxDelay(x int) func(*Client) {
	return func(client *Client) {
		client.BackoffMaxDelay = x
	}
}

// BackoffDelayFactor modifies the backoff delay factor from the default of 3.
func BackoffDelayFactor(x float64) func(*Client) {
	return func(client *Client) {
		client.BackoffDelayFactor = x
	}
}

// Logging enables debug logging. Default value is false.
func Logging(x bool) func(*Client) {
	return func(client *Client) {
		client.Logging = x
	}
}

// NewReq creates a new Req request for this client.
func (client Client) NewReq(method, uri string, body io.Reader, mods ...func(*Req)) Req {
	httpReq, _ := http.NewRequest(method, client.Url+uri+".json", body)
	req := Req{
		HttpReq:    httpReq,
		Refresh:    true,
		LogPayload: true,
	}
	for _, mod := range mods {
		mod(&req)
	}
	return req
}

// Do makes a request.
// Requests for Do are built ouside of the client, e.g.
//
//	req := client.NewReq("GET", "/api/class/fvBD", nil)
//	res := client.Do(req)
func (client *Client) Do(req Req) (Res, error) {
	// retain the request body across multiple attempts
	var body []byte
	if req.HttpReq.Body != nil {
		body, _ = io.ReadAll(req.HttpReq.Body)
	}

	var res Res

	for attempts := 0; ; attempts++ {
		req.HttpReq.Body = io.NopCloser(bytes.NewBuffer(body))
		if req.LogPayload && client.Logging {
			log.Printf("[DEBUG] HTTP Request: %s, %s, %s", req.HttpReq.Method, req.HttpReq.URL, req.HttpReq.Body)
		} else if client.Logging {
			log.Printf("[DEBUG] HTTP Request: %s, %s", req.HttpReq.Method, req.HttpReq.URL)
		}

		httpRes, err := client.HttpClient.Do(req.HttpReq)
		if err != nil {
			if ok := client.Backoff(attempts); !ok {
				if client.Logging {
					log.Printf("[ERROR] HTTP Connection error occured: %+v", err)
					log.Printf("[DEBUG] Exit from Do method")
				}
				return Res{}, err
			} else {
				if client.Logging {
					log.Printf("[ERROR] HTTP Connection failed: %s, retries: %v", err, attempts)
				}
				continue
			}
		}

		defer httpRes.Body.Close()
		bodyBytes, err := io.ReadAll(httpRes.Body)
		if err != nil {
			if ok := client.Backoff(attempts); !ok {
				if client.Logging {
					log.Printf("[ERROR] Cannot decode response body: %+v", err)
					log.Printf("[DEBUG] Exit from Do method")
				}
				return Res{}, err
			} else {
				if client.Logging {
					log.Printf("[ERROR] Cannot decode response body: %s, retries: %v", err, attempts)
				}
				continue
			}
		}
		res = Res(gjson.ParseBytes(bodyBytes))
		if req.LogPayload && client.Logging {
			log.Printf("[DEBUG] HTTP Response: %s", res.Raw)
		}

		if (httpRes.StatusCode < 500 || httpRes.StatusCode > 504) && httpRes.StatusCode != 405 {
			if client.Logging {
				log.Printf("[DEBUG] Exit from Do method")
			}
			break
		} else {
			if ok := client.Backoff(attempts); !ok {
				if client.Logging {
					log.Printf("[ERROR] HTTP Request failed: StatusCode %v", httpRes.StatusCode)
					log.Printf("[DEBUG] Exit from Do method")
				}
				return Res{}, fmt.Errorf("HTTP Request failed: StatusCode %v", httpRes.StatusCode)
			} else {
				if client.Logging {
					log.Printf("[ERROR] HTTP Request failed: StatusCode %v, Retries: %v", httpRes.StatusCode, attempts)
				}
				continue
			}
		}
	}

	errCode := res.Get("imdata.0.error.attributes.code").Str
	if errCode != "" {
		if client.Logging {
			log.Printf("[ERROR] JSON error: %s", res.Raw)
		}
		return res, fmt.Errorf("JSON error: %s", res.Raw)
	}
	return res, nil
}

// Get makes a GET request and returns a GJSON result.
// Results will be the raw data structure as returned by the APIC, wrapped in imdata, e.g.
//
//	{
//	  "imdata": [
//	    {
//	      "fvTenant": {
//	        "attributes": {
//	          "dn": "uni/tn-mytenant",
//	          "name": "mytenant",
//	        }
//	      }
//	    }
//	  ],
//	  "totalCount": "1"
//	}
func (client *Client) Get(path string, mods ...func(*Req)) (Res, error) {
	req := client.NewReq("GET", path, nil, mods...)
	client.Authenticate()
	return client.Do(req)
}

// GetClass makes a GET request by class and unwraps the results.
// Result is removed from imdata, but still wrapped in Class.attributes, e.g.
//
//	[
//	  {
//	    "fvTenant": {
//	      "attributes": {
//	        "dn": "uni/tn-mytenant",
//	        "name": "mytenant",
//	      }
//	    }
//	  }
//	]
func (client *Client) GetClass(class string, mods ...func(*Req)) (Res, error) {
	res, err := client.Get(fmt.Sprintf("/api/class/%s", class), mods...)
	if err != nil {
		return res, err
	}
	return res.Get("imdata"), nil
}

// GetDn makes a GET request by DN.
// Result is removed from imdata and first result is removed from the list, e.g.
//
//	{
//	  "fvTenant": {
//	    "attributes": {
//	      "dn": "uni/tn-mytenant",
//	      "name": "mytenant",
//	    }
//	  }
//	}
func (client *Client) GetDn(dn string, mods ...func(*Req)) (Res, error) {
	res, err := client.Get(fmt.Sprintf("/api/mo/%s", dn), mods...)
	if err != nil {
		return res, err
	}
	return res.Get("imdata.0"), nil
}

// DeleteDn makes a DELETE request by DN.
func (client *Client) DeleteDn(dn string, mods ...func(*Req)) (Res, error) {
	req := client.NewReq("DELETE", fmt.Sprintf("/api/mo/%s", dn), nil, mods...)
	client.Authenticate()
	return client.Do(req)
}

// Post makes a POST request and returns a GJSON result.
// Hint: Use the Body struct to easily create POST body data.
func (client *Client) Post(dn, data string, mods ...func(*Req)) (Res, error) {
	req := client.NewReq("POST", fmt.Sprintf("/api/mo/%s", dn), strings.NewReader(data), mods...)
	client.Authenticate()
	return client.Do(req)
}

// Login authenticates to the APIC.
func (client *Client) Login() error {
	data := fmt.Sprintf(`{"aaaUser":{"attributes":{"name":"%s","pwd":"%s"}}}`,
		client.Usr,
		client.Pwd,
	)
	req := client.NewReq("POST", "/api/aaaLogin", strings.NewReader(data), NoRefresh, NoLogPayload)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	client.Token = res.Get("imdata.0.aaaLogin.attributes.token").Str
	client.LastRefresh = time.Now()
	return nil
}

// Refresh refreshes the authentication token.
// Note that this will be handled automatically be default.
// Refresh will be checked every request and the token will be refreshed after 8 minutes.
// Pass aci.NoRefresh to prevent automatic refresh handling and handle it directly instead.
func (client *Client) Refresh() error {
	res, err := client.Get("/api/aaaRefresh", NoRefresh, NoLogPayload)
	if err != nil {
		return err
	}
	client.Token = res.Get("imdata.0.aaaRefresh.attributes.token").Str
	client.LastRefresh = time.Now()
	return nil
}

// Login if no token available or refresh the token if older than 480 seconds.
func (client *Client) Authenticate() error {
	var err error
	client.AuthenticationMutex.Lock()
	if client.Token == "" {
		err = client.Login()
	} else if time.Since(client.LastRefresh) > 480*time.Second {
		err = client.Refresh()
	}
	client.AuthenticationMutex.Unlock()
	return err
}

// Backoff waits following an exponential backoff algorithm
func (client *Client) Backoff(attempts int) bool {
	if client.Logging {
		log.Printf("[DEBUG] Begining backoff method: attempts %v on %v", attempts, client.MaxRetries)
	}
	if attempts >= client.MaxRetries {
		if client.Logging {
			log.Printf("[DEBUG] Exit from backoff method with return value false")
		}
		return false
	}

	minDelay := time.Duration(client.BackoffMinDelay) * time.Second
	maxDelay := time.Duration(client.BackoffMaxDelay) * time.Second

	min := float64(minDelay)
	backoff := min * math.Pow(client.BackoffDelayFactor, float64(attempts))
	if backoff > float64(maxDelay) {
		backoff = float64(maxDelay)
	}
	backoff = (rand.Float64()/2+0.5)*(backoff-min) + min
	backoffDuration := time.Duration(backoff)
	if client.Logging {
		log.Printf("[TRACE] Starting sleeping for %v", backoffDuration.Round(time.Second))
	}
	time.Sleep(backoffDuration)
	if client.Logging {
		log.Printf("[DEBUG] Exit from backoff method with return value true")
	}
	return true
}
