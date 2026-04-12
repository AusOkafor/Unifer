package identity

import (
	"math"
	"strings"

	"merger/backend/internal/models"
	"merger/backend/internal/utils"
)

// Score represents pairwise similarity between two customers.
type Score struct {
	EmailSim   float64
	NameSim    float64
	PhoneSim   float64
	Combined   float64
}

// ScorePair computes a combined similarity score between two cached customers.
// Weights: email 40%, name 35%, phone 15%, phone bonus 10%.
func ScorePair(a, b *models.CustomerCache) Score {
	s := Score{}

	emailA := utils.NormalizeEmail(a.Email)
	emailB := utils.NormalizeEmail(b.Email)
	s.EmailSim = emailSimilarity(emailA, emailB)

	nameA := utils.NormalizeName(a.Name)
	nameB := utils.NormalizeName(b.Name)
	s.NameSim = jaroWinkler(nameA, nameB)

	phoneA := utils.NormalizePhone(a.Phone)
	phoneB := utils.NormalizePhone(b.Phone)
	if phoneA != "" && phoneB != "" {
		if phoneA == phoneB {
			s.PhoneSim = 1.0
		} else {
			// partial match if one is suffix of the other (local vs international format)
			if strings.HasSuffix(phoneA, phoneB) || strings.HasSuffix(phoneB, phoneA) {
				s.PhoneSim = 0.8
			}
		}
	}

	s.Combined = 0.40*s.EmailSim + 0.35*s.NameSim + 0.25*s.PhoneSim
	return s
}

// emailSimilarity returns a similarity score for two normalized email addresses.
func emailSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1.0
	}
	// Same domain, different local part → partial match
	domainA := utils.EmailDomain(a)
	domainB := utils.EmailDomain(b)
	if domainA == domainB && domainA != "" {
		// Score based on Levenshtein similarity of the full address
		return 0.5 * levenshteinSim(a, b)
	}
	return levenshteinSim(a, b)
}

// levenshteinSim returns 1 - (edit_distance / max_length).
func levenshteinSim(a, b string) float64 {
	d := levenshtein(a, b)
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	if max == 0 {
		return 1.0
	}
	return 1.0 - float64(d)/float64(max)
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			dp[i][j] = min3(dp[i-1][j]+1, dp[i][j-1]+1, dp[i-1][j-1]+cost)
		}
	}
	return dp[la][lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// jaroWinkler computes the Jaro-Winkler similarity between two strings.
func jaroWinkler(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	jaro := jaroSim(a, b)
	// Winkler prefix bonus (up to 4 chars)
	prefix := 0
	for i := 0; i < len(a) && i < len(b) && i < 4; i++ {
		if a[i] == b[i] {
			prefix++
		} else {
			break
		}
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

func jaroSim(a, b string) float64 {
	la, lb := len(a), len(b)
	matchDist := int(math.Max(float64(la), float64(lb))/2) - 1
	if matchDist < 0 {
		matchDist = 0
	}

	aMatched := make([]bool, la)
	bMatched := make([]bool, lb)
	matches := 0

	for i := 0; i < la; i++ {
		start := i - matchDist
		if start < 0 {
			start = 0
		}
		end := i + matchDist + 1
		if end > lb {
			end = lb
		}
		for j := start; j < end; j++ {
			if !bMatched[j] && a[i] == b[j] {
				aMatched[i] = true
				bMatched[j] = true
				matches++
				break
			}
		}
	}

	if matches == 0 {
		return 0.0
	}

	transpositions := 0
	k := 0
	for i := 0; i < la; i++ {
		if aMatched[i] {
			for !bMatched[k] {
				k++
			}
			if a[i] != b[k] {
				transpositions++
			}
			k++
		}
	}

	m := float64(matches)
	return (m/float64(la) + m/float64(lb) + (m-float64(transpositions)/2)/m) / 3
}
