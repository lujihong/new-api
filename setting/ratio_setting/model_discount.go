package ratio_setting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

const (
	modelDiscountMaxJSONBytes  = 1 << 20
	modelDiscountMaxRules      = 10000
	modelDiscountMaxModelBytes = 200
)

// ModelDiscountRules is the JSON schema for the single ModelDiscountRules option.
// Missing sections mean empty rules. User keys are canonical positive int IDs.
// It is a transfer type; no mutable maps from a published snapshot are exposed.
type ModelDiscountRules struct {
	Groups map[string]map[string]float64 `json:"groups"`
	Users  map[string]map[string]float64 `json:"users"`
}

// ModelDiscountSelection selects only a discount, not an existing group ratio.
// The caller multiplies Factor by its existing final group ratio once.
type ModelDiscountSelection struct {
	Factor   float64 `json:"factor"`
	Source   string  `json:"source"`
	Revision string  `json:"revision"`
	Model    string  `json:"model"`
}

// ModelDiscountSnapshot is immutable after construction. Capture it once at the
// start of a request and retain it across retries. Its zero value is also safe.
type ModelDiscountSnapshot struct {
	rules     ModelDiscountRules
	canonical string
	revision  string
}

var emptyModelDiscountSnapshot = newModelDiscountSnapshot(ModelDiscountRules{
	Groups: map[string]map[string]float64{},
	Users:  map[string]map[string]float64{},
})

var currentModelDiscountSnapshot atomic.Pointer[ModelDiscountSnapshot]

// ValidateModelDiscountRulesJSON validates without changing the active rules.
func ValidateModelDiscountRulesJSON(raw string) error {
	_, err := parseModelDiscountRules(raw)
	return err
}

// CanonicalModelDiscountRulesJSON normalizes without publishing a new snapshot.
func CanonicalModelDiscountRulesJSON(raw string) (string, error) {
	rules, err := parseModelDiscountRules(raw)
	if err != nil {
		return "", err
	}
	return newModelDiscountSnapshot(rules).canonical, nil
}

// UpdateModelDiscountRulesJSON replaces the whole option atomically, only after
// complete validation. An invalid update leaves the previous snapshot intact.
func UpdateModelDiscountRulesJSON(raw string) error {
	rules, err := parseModelDiscountRules(raw)
	if err != nil {
		return err
	}
	currentModelDiscountSnapshot.Store(newModelDiscountSnapshot(rules))
	return nil
}

// ModelDiscountRulesJSONString returns detached canonical JSON, never live maps.
func ModelDiscountRulesJSONString() string {
	return CaptureModelDiscountRules().canonical
}

// CaptureModelDiscountRules pins the current rules for the lifetime of a request.
func CaptureModelDiscountRules() *ModelDiscountSnapshot {
	if snapshot := currentModelDiscountSnapshot.Load(); snapshot != nil {
		return snapshot
	}
	return emptyModelDiscountSnapshot
}

// Lookup matches the complete origin model ID verbatim, without wildcard,
// routing alias, suffix normalization or inheritance. A personal match wins,
// including factors 0 and 1; otherwise the user's group is tried, then factor 1.
func (snapshot *ModelDiscountSnapshot) Lookup(userID int, userGroup, originModel string) ModelDiscountSelection {
	if snapshot == nil || snapshot.revision == "" {
		snapshot = emptyModelDiscountSnapshot
	}
	selection := ModelDiscountSelection{
		Factor: 1, Source: "default", Revision: snapshot.revision, Model: originModel,
	}
	if userID > 0 {
		if factor, ok := snapshot.rules.Users[strconv.Itoa(userID)][originModel]; ok {
			selection.Factor, selection.Source = factor, "user"
			return selection
		}
	}
	if factor, ok := snapshot.rules.Groups[userGroup][originModel]; ok {
		selection.Factor, selection.Source = factor, "group"
	}
	return selection
}

// Only the strict parser (or the empty default above) supplies these owned maps.
func newModelDiscountSnapshot(rules ModelDiscountRules) *ModelDiscountSnapshot {
	// encoding/json sorts string map keys. Every factor is finite and in [0,1],
	// so marshaling this schema cannot fail.
	canonical, _ := json.Marshal(rules)
	digest := sha256.Sum256(canonical)
	return &ModelDiscountSnapshot{
		rules: rules, canonical: string(canonical), revision: hex.EncodeToString(digest[:]),
	}
}

func parseModelDiscountRules(raw string) (ModelDiscountRules, error) {
	rules := ModelDiscountRules{
		Groups: make(map[string]map[string]float64),
		Users:  make(map[string]map[string]float64),
	}
	if len(raw) > modelDiscountMaxJSONBytes {
		return rules, fmt.Errorf("ModelDiscountRules JSON exceeds %d bytes", modelDiscountMaxJSONBytes)
	}
	if !utf8.ValidString(raw) {
		return rules, fmt.Errorf("ModelDiscountRules JSON must be valid UTF-8")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	total := 0
	err := readModelDiscountObject(decoder, "ModelDiscountRules", func(section string) error {
		var owners map[string]map[string]float64
		switch section {
		case "groups":
			owners = rules.Groups
		case "users":
			owners = rules.Users
		default:
			return fmt.Errorf("unknown ModelDiscountRules key %q", section)
		}
		return readModelDiscountObject(decoder, section, func(owner string) error {
			if section == "users" {
				id, err := strconv.Atoi(owner)
				if err != nil || id <= 0 || strconv.Itoa(id) != owner {
					return fmt.Errorf("invalid user ID %q: expected canonical positive int", owner)
				}
			} else if owner == "" || strings.TrimSpace(owner) != owner {
				return fmt.Errorf("invalid group name %q", owner)
			}
			models := make(map[string]float64)
			err := readModelDiscountObject(decoder, section+"."+owner, func(model string) error {
				if model == "" || strings.TrimSpace(model) != model || len(model) > modelDiscountMaxModelBytes {
					return fmt.Errorf("invalid model name %q: expected 1-%d bytes without surrounding whitespace", model, modelDiscountMaxModelBytes)
				}
				total++
				if total > modelDiscountMaxRules {
					return fmt.Errorf("ModelDiscountRules exceeds %d rules", modelDiscountMaxRules)
				}
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				number, ok := token.(json.Number)
				if !ok {
					return fmt.Errorf("discount for %s.%s.%s must be a number", section, owner, model)
				}
				factor, err := number.Float64()
				if err != nil || math.IsNaN(factor) || math.IsInf(factor, 0) || factor < 0 || factor > 1 || !modelDiscountNumberInRange(number.String()) {
					return fmt.Errorf("discount for %s.%s.%s must be finite and in [0,1]", section, owner, model)
				}
				// Canonicalize negative zero as well as other numeric spellings.
				if factor == 0 {
					factor = 0
				}
				models[model] = factor
				return nil
			})
			if err != nil {
				return err
			}
			// Empty owners have no lookup effect; normalize them away so all
			// representations of an empty rule set share the same revision.
			if len(models) != 0 {
				owners[owner] = models
			}
			return nil
		})
	})
	if err != nil {
		return rules, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return rules, fmt.Errorf("ModelDiscountRules must contain exactly one JSON object")
		}
		return rules, err
	}
	return rules, nil
}

// modelDiscountNumberInRange checks a syntactically valid JSON number before
// float64 rounding can turn a negative underflow into zero or a value above one
// into one. Compare decimal magnitude without allocating exponent-sized values.
func modelDiscountNumberInRange(raw string) bool {
	negative := strings.HasPrefix(raw, "-")
	mantissa := strings.TrimPrefix(raw, "-")
	exponent := 0
	if index := strings.IndexAny(mantissa, "eE"); index >= 0 {
		exponentText := mantissa[index+1:]
		mantissa = mantissa[:index]
		exponentNegative := strings.HasPrefix(exponentText, "-")
		exponentText = strings.TrimLeft(exponentText, "+-")
		// No mantissa in a bounded input can offset an exponent this large.
		for _, digit := range exponentText {
			exponent = exponent*10 + int(digit-'0')
			if exponent > modelDiscountMaxJSONBytes {
				exponent = modelDiscountMaxJSONBytes + 1
				break
			}
		}
		if exponentNegative {
			exponent = -exponent
		}
	}
	decimalPosition := strings.IndexByte(mantissa, '.')
	if decimalPosition < 0 {
		decimalPosition = len(mantissa)
	}
	digitPosition, firstNonzero, firstDigit, laterNonzero := 0, -1, byte(0), false
	for index := 0; index < len(mantissa); index++ {
		digit := mantissa[index]
		if digit == '.' {
			continue
		}
		if digit != '0' {
			if firstNonzero < 0 {
				firstNonzero, firstDigit = digitPosition, digit
			} else {
				laterNonzero = true
			}
		}
		digitPosition++
	}
	if firstNonzero < 0 {
		return true // Includes negative zero with any decimal exponent.
	}
	if negative {
		return false
	}
	order := decimalPosition - firstNonzero - 1 + exponent
	return order < 0 || (order == 0 && firstDigit == '1' && !laterNonzero)
}

// readModelDiscountObject checks duplicates on decoded keys (so escaped aliases
// collide too). Fixed schema callbacks bound nesting without a recursive walk.
func readModelDiscountObject(decoder *json.Decoder, path string, value func(string) error) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("%s must be a non-null JSON object", path)
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("%s must have string keys", path)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON key %q in %s", key, path)
		}
		seen[key] = struct{}{}
		if err := value(key); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('}') {
		return fmt.Errorf("%s must end with an object delimiter", path)
	}
	return nil
}
