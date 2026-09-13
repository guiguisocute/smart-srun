package auth

import (
	"encoding/json"
	"strings"

	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

// Result is what one request to the gateway produced.
//
// State is deliberately not a bool. A gateway that says "ok" has said something
// about itself, not about whose session is now on the line, and spec 04 keeps
// those apart all the way through.
type Result struct {
	State domain.AuthState
	// AlreadyOnline is the gateway's own "this line already has a session"
	// answer. It is not a success: the session may belong to someone else, and
	// which it is has to be asked separately.
	AlreadyOnline bool
	// GatewayCode and GatewayMessage are what the portal said, kept for the
	// log. They are the portal's words, not this program's, and are never
	// shown to a user as if they were an explanation this program wrote.
	GatewayCode    string
	GatewayMessage string
	// Identity is who the gateway named, when it named anyone.
	Identity string
	// ClientIP is the address the gateway associated with the session.
	ClientIP string
}

// Identity is the answer to "who is online on this line".
type Identity struct {
	// Present is false when nobody is authenticated. That is different from
	// not knowing, which is an error rather than an Identity.
	Present bool
	// Username as the gateway reported it.
	Username string
	ClientIP string
	// MatchesExpected is whether this is the account that was asked about.
	//
	// Compared against both the bare account and the suffixed form, because a
	// gateway may report either and treating "2020123456" as a different user
	// from "2020123456@cmcc" would have this program log itself out.
	MatchesExpected bool
}

// State turns an identity into the dimension spec 04 defines.
func (i Identity) State() domain.AuthState {
	switch {
	case !i.Present:
		return domain.AuthUnknown
	case i.MatchesExpected:
		return domain.AuthVerifiedSelf
	default:
		return domain.AuthVerifiedOther
	}
}

// gatewayAnswer is the shape SRun portals reply with. Fields vary between
// deployments, so everything is optional and nothing is required to parse.
type gatewayAnswer struct {
	Error       string          `json:"error"`
	ErrorMsg    string          `json:"error_msg"`
	ECode       json.RawMessage `json:"ecode"`
	ClientIP    string          `json:"client_ip"`
	OnlineIP    string          `json:"online_ip"`
	UserName    string          `json:"user_name"`
	SuccessMsg  string          `json:"suc_msg"`
	ResultValue json.RawMessage `json:"res"`
}

// interpret reads a login or logout reply.
//
// The three answers that matter are "ok", "already online", and everything
// else. Only the first two are not failures, and the second is not a success
// either -- it is a question about whose session that is.
func interpret(payload json.RawMessage, expected string) (Result, error) {
	var answer gatewayAnswer
	if err := json.Unmarshal(payload, &answer); err != nil {
		return Result{}, domain.Errorf(domain.CodeProtocolInvalid,
			"认证网关的响应无法解析").Wrap(err)
	}

	result := Result{
		GatewayCode:    strings.TrimSpace(answer.Error),
		GatewayMessage: strings.TrimSpace(answer.ErrorMsg),
		Identity:       trimIdentity(answer.UserName),
		ClientIP:       firstNonEmpty(answer.ClientIP, answer.OnlineIP),
	}
	if result.GatewayCode == "" {
		result.GatewayCode = strings.Trim(string(answer.ResultValue), `"`)
	}

	switch {
	case isOK(result.GatewayCode):
		result.State = domain.AuthAccepted
	case isAlreadyOnline(result.GatewayCode, result.GatewayMessage):
		result.AlreadyOnline = true
		// Not Accepted. Something is online; whether it is this account is a
		// separate question, and answering it optimistically here is how a
		// user gets told they are logged in on somebody else's session.
		result.State = domain.AuthUnknown
	default:
		result.State = domain.AuthRejected
	}
	return result, nil
}

// readIdentity reads the online-status reply.
func readIdentity(payload json.RawMessage, expected string) (Identity, error) {
	var answer gatewayAnswer
	if err := json.Unmarshal(payload, &answer); err != nil {
		return Identity{}, domain.Errorf(domain.CodeProtocolInvalid,
			"在线状态无法解析").Wrap(err)
	}

	username := trimIdentity(answer.UserName)
	if username == "" {
		// "not_online_error" and an empty user name both mean nobody is
		// authenticated. An empty identity is not a match for anything, which
		// is why this returns before the comparison.
		return Identity{Present: false}, nil
	}

	return Identity{
		Present:         true,
		Username:        username,
		ClientIP:        firstNonEmpty(answer.ClientIP, answer.OnlineIP),
		MatchesExpected: sameAccount(username, expected),
	}, nil
}

// sameAccount compares two account names allowing for the operator suffix.
//
// A gateway may report "2020123456" for a login sent as "2020123456@cmcc", or
// the other way round. Treating those as different users would make this
// program log itself out and try again forever; treating two genuinely
// different accounts as the same would make it claim somebody else's session.
// So the comparison is: equal, or equal up to one side's suffix.
func sameAccount(reported, expected string) bool {
	reported = trimIdentity(reported)
	expected = trimIdentity(expected)
	if reported == "" || expected == "" {
		return false
	}
	if reported == expected {
		return true
	}
	return bareAccount(reported) == bareAccount(expected)
}

// bareAccount drops an operator suffix.
func bareAccount(value string) string {
	if at := strings.IndexByte(value, '@'); at > 0 {
		return value[:at]
	}
	return value
}

// isOK recognises the gateway's success code.
func isOK(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), "ok")
}

// isAlreadyOnline recognises the "this line already has a session" answer.
//
// Two spellings, because deployments differ and the baseline handled both. The
// message is checked as well as the code: some portals put it only there.
func isAlreadyOnline(code, message string) bool {
	haystack := strings.ToLower(code + " " + message)
	for _, marker := range []string{"ip_already_online_error", "already online",
		"already_online"} {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
