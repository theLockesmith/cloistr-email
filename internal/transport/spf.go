// Package transport provides email transport mechanisms.
package transport

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
)

// SPFResult represents the result of an SPF check
type SPFResult string

const (
	// SPFPass means the sender is authorized
	SPFPass SPFResult = "pass"

	// SPFFail means the sender is not authorized
	SPFFail SPFResult = "fail"

	// SPFSoftFail means the sender is probably not authorized
	SPFSoftFail SPFResult = "softfail"

	// SPFNeutral means no policy assertion
	SPFNeutral SPFResult = "neutral"

	// SPFNone means no SPF record found
	SPFNone SPFResult = "none"

	// SPFTempError means a temporary error occurred
	SPFTempError SPFResult = "temperror"

	// SPFPermError means a permanent error occurred (invalid record)
	SPFPermError SPFResult = "permerror"
)

// SPFValidator validates SPF records for incoming mail
type SPFValidator struct {
	logger   *zap.Logger
	resolver *net.Resolver

	// Configuration
	lookupLimit int           // Max DNS lookups per check (default: 10)
	timeout     time.Duration // DNS lookup timeout
}

// SPFValidatorOption configures the SPF validator
type SPFValidatorOption func(*SPFValidator)

// WithSPFLookupLimit sets the maximum DNS lookups per SPF check
func WithSPFLookupLimit(limit int) SPFValidatorOption {
	return func(v *SPFValidator) {
		v.lookupLimit = limit
	}
}

// WithSPFTimeout sets the DNS lookup timeout
func WithSPFTimeout(timeout time.Duration) SPFValidatorOption {
	return func(v *SPFValidator) {
		v.timeout = timeout
	}
}

// NewSPFValidator creates a new SPF validator
func NewSPFValidator(logger *zap.Logger, opts ...SPFValidatorOption) *SPFValidator {
	v := &SPFValidator{
		logger:      logger,
		lookupLimit: 10,
		timeout:     10 * time.Second,
	}

	for _, opt := range opts {
		opt(v)
	}

	return v
}

// SPFCheckResult contains the full result of an SPF check
type SPFCheckResult struct {
	Result      SPFResult
	Domain      string
	Explanation string
	Record      string
}

// Check validates SPF for the given parameters
func (v *SPFValidator) Check(ctx context.Context, ip string, domain string, sender string) *SPFCheckResult {
	result := &SPFCheckResult{
		Domain: domain,
	}

	v.logger.Debug("Checking SPF",
		zap.String("ip", ip),
		zap.String("domain", domain),
		zap.String("sender", sender))

	// Parse IP address
	clientIP := net.ParseIP(ip)
	if clientIP == nil {
		result.Result = SPFPermError
		result.Explanation = "invalid client IP"
		return result
	}

	// Look up SPF record
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	record, err := v.lookupSPF(ctx, domain)
	if err != nil {
		if isTemporaryError(err) {
			result.Result = SPFTempError
			result.Explanation = err.Error()
		} else {
			result.Result = SPFNone
			result.Explanation = "no SPF record found"
		}
		return result
	}

	result.Record = record

	// Parse and evaluate the SPF record
	spfResult, explanation := v.evaluateSPF(ctx, record, clientIP, domain, sender, 0)
	result.Result = spfResult
	result.Explanation = explanation

	v.logger.Debug("SPF check complete",
		zap.String("domain", domain),
		zap.String("result", string(result.Result)),
		zap.String("explanation", explanation))

	return result
}

// lookupSPF retrieves the SPF record for a domain
func (v *SPFValidator) lookupSPF(ctx context.Context, domain string) (string, error) {
	var resolver *net.Resolver
	if v.resolver != nil {
		resolver = v.resolver
	} else {
		resolver = net.DefaultResolver
	}

	txts, err := resolver.LookupTXT(ctx, domain)
	if err != nil {
		return "", err
	}

	// Find SPF record (starts with "v=spf1")
	for _, txt := range txts {
		if strings.HasPrefix(strings.ToLower(txt), "v=spf1") {
			return txt, nil
		}
	}

	return "", fmt.Errorf("no SPF record found")
}

// evaluateSPF evaluates an SPF record
func (v *SPFValidator) evaluateSPF(ctx context.Context, record string, clientIP net.IP, domain, sender string, depth int) (SPFResult, string) {
	if depth > v.lookupLimit {
		return SPFPermError, "too many DNS lookups"
	}

	// Parse SPF record
	parts := strings.Fields(record)
	if len(parts) == 0 || !strings.EqualFold(parts[0], "v=spf1") {
		return SPFPermError, "invalid SPF record"
	}

	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Determine qualifier
		qualifier := '+'
		mechanism := part
		if len(part) > 0 && (part[0] == '+' || part[0] == '-' || part[0] == '~' || part[0] == '?') {
			qualifier = rune(part[0])
			mechanism = part[1:]
		}

		// Evaluate mechanism
		match, err := v.evaluateMechanism(ctx, mechanism, clientIP, domain, sender, depth)
		if err != nil {
			v.logger.Debug("SPF mechanism error",
				zap.String("mechanism", mechanism),
				zap.Error(err))
			continue
		}

		if match {
			switch qualifier {
			case '+':
				return SPFPass, fmt.Sprintf("matched %s", mechanism)
			case '-':
				return SPFFail, fmt.Sprintf("matched %s", mechanism)
			case '~':
				return SPFSoftFail, fmt.Sprintf("matched %s", mechanism)
			case '?':
				return SPFNeutral, fmt.Sprintf("matched %s", mechanism)
			}
		}
	}

	// Default result is neutral
	return SPFNeutral, "no mechanism matched"
}

// evaluateMechanism evaluates a single SPF mechanism
func (v *SPFValidator) evaluateMechanism(ctx context.Context, mechanism string, clientIP net.IP, domain, sender string, depth int) (bool, error) {
	mechanism = strings.ToLower(mechanism)

	// Handle "all" mechanism
	if mechanism == "all" {
		return true, nil
	}

	// Handle "ip4" mechanism
	if strings.HasPrefix(mechanism, "ip4:") {
		return v.matchIP4(mechanism[4:], clientIP)
	}

	// Handle "ip6" mechanism
	if strings.HasPrefix(mechanism, "ip6:") {
		return v.matchIP6(mechanism[4:], clientIP)
	}

	// Handle "a" mechanism
	if mechanism == "a" || strings.HasPrefix(mechanism, "a:") || strings.HasPrefix(mechanism, "a/") {
		return v.matchA(ctx, mechanism, clientIP, domain)
	}

	// Handle "mx" mechanism
	if mechanism == "mx" || strings.HasPrefix(mechanism, "mx:") || strings.HasPrefix(mechanism, "mx/") {
		return v.matchMX(ctx, mechanism, clientIP, domain)
	}

	// Handle "include" mechanism
	if strings.HasPrefix(mechanism, "include:") {
		includeDomain := mechanism[8:]
		record, err := v.lookupSPF(ctx, includeDomain)
		if err != nil {
			return false, err
		}
		result, _ := v.evaluateSPF(ctx, record, clientIP, includeDomain, sender, depth+1)
		return result == SPFPass, nil
	}

	// Handle "redirect" modifier
	if strings.HasPrefix(mechanism, "redirect=") {
		redirectDomain := mechanism[9:]
		record, err := v.lookupSPF(ctx, redirectDomain)
		if err != nil {
			return false, err
		}
		result, _ := v.evaluateSPF(ctx, record, clientIP, redirectDomain, sender, depth+1)
		return result == SPFPass, nil
	}

	return false, nil
}

// matchIP4 checks if the client IP matches an IPv4 CIDR
func (v *SPFValidator) matchIP4(cidr string, clientIP net.IP) (bool, error) {
	// Add /32 if no prefix specified
	if !strings.Contains(cidr, "/") {
		cidr = cidr + "/32"
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, err
	}

	return network.Contains(clientIP), nil
}

// matchIP6 checks if the client IP matches an IPv6 CIDR
func (v *SPFValidator) matchIP6(cidr string, clientIP net.IP) (bool, error) {
	// Add /128 if no prefix specified
	if !strings.Contains(cidr, "/") {
		cidr = cidr + "/128"
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, err
	}

	return network.Contains(clientIP), nil
}

// matchA checks if the client IP matches the A/AAAA records
func (v *SPFValidator) matchA(ctx context.Context, mechanism string, clientIP net.IP, domain string) (bool, error) {
	targetDomain := domain

	// Parse domain from mechanism if specified
	if strings.HasPrefix(mechanism, "a:") {
		parts := strings.SplitN(mechanism[2:], "/", 2)
		targetDomain = parts[0]
	}

	var resolver *net.Resolver
	if v.resolver != nil {
		resolver = v.resolver
	} else {
		resolver = net.DefaultResolver
	}

	ips, err := resolver.LookupIPAddr(ctx, targetDomain)
	if err != nil {
		return false, err
	}

	for _, ip := range ips {
		if ip.IP.Equal(clientIP) {
			return true, nil
		}
	}

	return false, nil
}

// matchMX checks if the client IP matches the MX records
func (v *SPFValidator) matchMX(ctx context.Context, mechanism string, clientIP net.IP, domain string) (bool, error) {
	targetDomain := domain

	// Parse domain from mechanism if specified
	if strings.HasPrefix(mechanism, "mx:") {
		parts := strings.SplitN(mechanism[3:], "/", 2)
		targetDomain = parts[0]
	}

	var resolver *net.Resolver
	if v.resolver != nil {
		resolver = v.resolver
	} else {
		resolver = net.DefaultResolver
	}

	mxs, err := resolver.LookupMX(ctx, targetDomain)
	if err != nil {
		return false, err
	}

	for _, mx := range mxs {
		ips, err := resolver.LookupIPAddr(ctx, mx.Host)
		if err != nil {
			continue
		}

		for _, ip := range ips {
			if ip.IP.Equal(clientIP) {
				return true, nil
			}
		}
	}

	return false, nil
}

// isTemporaryError checks if a DNS error is temporary
func isTemporaryError(err error) bool {
	if dnsErr, ok := err.(*net.DNSError); ok {
		return dnsErr.Temporary()
	}
	return false
}
