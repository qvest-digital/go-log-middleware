package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/bshuster-repo/logrus-logstash-hook"
	"github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var Logger *logrus.Logger

// The of cookies which should not be logged
var AccessLogCookiesBlacklist []string

var LifecycleEnvVars = []string{"BUILD_NUMBER", "BUILD_HASH", "BUILD_DATE"}

// List of query params that should be anonymized
var AnonymizedQueryParams []string

func init() {
	_ = Set("info", false)
}

// Set creates a new Logger with the matching specification
func Set(level string, textLogging bool) error {
	l, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}

	logger := logrus.New()
	if textLogging {
		logger.Formatter = &logrus.TextFormatter{}
	} else {
		logger.Formatter = logrustash.DefaultFormatter(logrus.Fields{})
	}
	logger.Level = l
	Logger = logger
	return nil
}

// Access logs an access entry with call duration and status code
func Access(r *http.Request, start time.Time, statusCode int) {
	e := access(r, start, statusCode, nil)

	var msg string
	if len(r.URL.RawQuery) == 0 {
		msg = fmt.Sprintf("%v ->%v %v", statusCode, r.Method, r.URL.Path)
	} else {
		msg = fmt.Sprintf("%v ->%v %v?...", statusCode, r.Method, r.URL.Path)
	}

	if statusCode >= 200 && statusCode <= 399 {
		e.Info(msg)
	} else if statusCode >= 400 && statusCode <= 499 {
		e.Warn(msg)
	} else {
		e.Error(msg)
	}
}

// AccessError logs an error while accessing
func AccessError(r *http.Request, start time.Time, err error) {
	e := access(r, start, 0, err)
	e.Errorf("ERROR ->%v %v", r.Method, r.URL.Path)
}

func access(r *http.Request, start time.Time, statusCode int, err error) *logrus.Entry {
	fields := logrus.Fields{
		"type":       "access",
		"@timestamp": start,
		"remote_ip":  getRemoteIp(r),
		"host":       r.Host,
		"url":        buildFullPath(r),
		"method":     r.Method,
		"proto":      r.Proto,
		"duration":   time.Since(start).Nanoseconds() / 1000000,
		"User_Agent": r.Header.Get("User-Agent"),
	}

	if statusCode != 0 {
		fields["response_status"] = statusCode
	}

	if err != nil {
		fields[logrus.ErrorKey] = err.Error()
	}

	setCorrelationIds(fields, r.Header)

	cookies := map[string]string{}
	for _, c := range r.Cookies() {
		if !contains(AccessLogCookiesBlacklist, c.Name) {
			cookies[c.Name] = c.Value
		}
	}
	if len(cookies) > 0 {
		fields["cookies"] = cookies
	}

	return Logger.WithFields(fields)
}

// Call logs the result of an outgoing call
func Call(r *http.Request, resp *http.Response, start time.Time, err error) {
	fields := logrus.Fields{
		"type":       "call",
		"@timestamp": start,
		"host":       r.Host,
		"url":        buildFullPath(r),
		"full_url":   buildFullUrl(r),
		"method":     r.Method,
		"duration":   time.Since(start).Nanoseconds() / 1000000,
	}

	setCorrelationIds(fields, r.Header)

	if err != nil {
		fields[logrus.ErrorKey] = err.Error()
		Logger.WithFields(fields).Error(err)
		return
	}

	if resp != nil {
		fields["response_status"] = resp.StatusCode
		fields["content_type"] = resp.Header.Get("Content-Type")
		e := Logger.WithFields(fields)
		msg := fmt.Sprintf("%v %v-> %v", resp.StatusCode, r.Method, buildFullUrl(r))

		if resp.StatusCode >= 200 && resp.StatusCode <= 399 {
			e.Info(msg)
		} else if resp.StatusCode >= 400 && resp.StatusCode <= 499 {
			e.Warn(msg)
		} else {
			e.Error(msg)
		}
		return
	}

	Logger.WithFields(fields).Warn("call, but no response given")
}

// Cacheinfo logs the hit information a accessing a ressource
func Cacheinfo(url string, hit bool) {
	var msg string
	if hit {
		msg = fmt.Sprintf("cache hit: %v", url)
	} else {
		msg = fmt.Sprintf("cache miss: %v", url)
	}
	Logger.WithFields(
		logrus.Fields{
			"type": "cacheinfo",
			"url":  url,
			"hit":  hit,
		}).
		Debug(msg)
}

// Return a log entry for application logs,
// pre-filled with the correlation ids out of the supplied request.
func Application(h http.Header) *logrus.Entry {
	fields := logrus.Fields{
		"type": "application",
	}
	setCorrelationIds(fields, h)
	return Logger.WithFields(fields)
}

// LifecycleStart logs the start of an application
// with the configuration struct or map as paramter.
func LifecycleStart(appName string, args interface{}) {
	fields := logrus.Fields{}

	jsonString, err := json.Marshal(args)
	if err == nil {
		err := json.Unmarshal(jsonString, &fields)
		if err != nil {
			fields["parse_error"] = err.Error()
		}
	}
	fields["type"] = "lifecycle"
	fields["event"] = "start"
	for _, env := range LifecycleEnvVars {
		if os.Getenv(env) != "" {
			fields[strings.ToLower(env)] = os.Getenv(env)
		}
	}

	Logger.WithFields(fields).Infof("starting application: %v", appName)
}

// LifecycleStop logs the stop of an application
func LifecycleStop(appName string, signal os.Signal, err error) {
	fields := logrus.Fields{
		"type":  "lifecycle",
		"event": "stop",
	}
	if signal != nil {
		fields["signal"] = signal.String()
	}

	if os.Getenv("BUILD_NUMBER") != "" {
		fields["build_number"] = os.Getenv("BUILD_NUMBER")
	}

	if err != nil {
		Logger.WithFields(fields).
			WithError(err).
			Errorf("stopping application: %v (%v)", appName, err)
	} else {
		Logger.WithFields(fields).Infof("stopping application: %v (%v)", appName, signal)
	}
}

func getRemoteIp(r *http.Request) string {
	if r.Header.Get("X-Cluster-Client-Ip") != "" {
		return r.Header.Get("X-Cluster-Client-Ip")
	}
	if r.Header.Get("X-Real-Ip") != "" {
		return r.Header.Get("X-Real-Ip")
	}
	return strings.Split(r.RemoteAddr, ":")[0]
}

func setCorrelationIds(fields logrus.Fields, h http.Header) {
	correlationId := GetCorrelationId(h)
	if correlationId != "" {
		fields["correlation_id"] = correlationId
	}
	userCorrelationId := GetUserCorrelationId(h)
	if userCorrelationId != "" {
		fields["user_correlation_id"] = userCorrelationId
	}
}

func buildFullPath(r *http.Request) string {
	queryParams := make(url.Values, len(r.URL.Query()))

	for key, value := range r.URL.Query() {
		if contains(AnonymizedQueryParams, key) {
			queryParams[key] = []string{"*****"}
		} else {
			queryParams[key] = value
		}
	}

	queryString, _ := url.QueryUnescape(queryParams.Encode())
	if queryString != "" {
		return fmt.Sprintf("%s?%s", r.URL.Path, queryString)
	} else {
		return fmt.Sprintf("%s", r.URL.Path)
	}

}

func buildFullUrl(r *http.Request) string {
	var buffer bytes.Buffer
	buffer.WriteString(r.URL.Scheme + "://")
	buffer.WriteString(r.URL.Hostname())
	if r.URL.Port() != "" {
		buffer.WriteString(":" + r.URL.Port())
	}
	buffer.WriteString(buildFullPath(r))

	return buffer.String()
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
