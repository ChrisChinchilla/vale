package lint

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/ValeLint/vale/check"
	"github.com/ValeLint/vale/core"
	"github.com/stretchr/testify/assert"
)

func TestGenderBias(t *testing.T) {
	reToMatches := map[string][]string{
		"(?:alumna|alumnus)":          {"alumna", "alumnus"},
		"(?:alumnae|alumni)":          {"alumnae", "alumni"},
		"(?:mother|father)land":       {"motherland", "fatherland"},
		"air(?:m[ae]n|wom[ae]n)":      {"airman", "airwoman", "airmen", "airwomen"},
		"anchor(?:m[ae]n|wom[ae]n)":   {"anchorman", "anchorwoman", "anchormen", "anchorwomen"},
		"camera(?:m[ae]n|wom[ae]n)":   {"cameraman", "camerawoman", "cameramen", "camerawomen"},
		"chair(?:m[ae]n|wom[ae]n)":    {"chairman", "chairwoman", "chairmen", "chairwomen"},
		"congress(?:m[ae]n|wom[ae]n)": {"congressman", "congresswoman", "congressmen", "congresswomen"},
		"door(?:m[ae]n|wom[ae]n)":     {"doorman", "doorwoman", "doormen", "doorwomen"},
		"drafts(?:m[ae]n|wom[ae]n)":   {"draftsman", "draftswoman", "draftsmen", "draftswomen"},
		"fire(?:m[ae]n|wom[ae]n)":     {"fireman", "firewoman", "firemen", "firewomen"},
		"fisher(?:m[ae]n|wom[ae]n)":   {"fisherman", "fisherwoman", "fishermen", "fisherwomen"},
		"fresh(?:m[ae]n|wom[ae]n)":    {"freshman", "freshwoman", "freshmen", "freshwomen"},
		"garbage(?:m[ae]n|wom[ae]n)":  {"garbageman", "garbagewoman", "garbagemen", "garbagewomen"},
		"mail(?:m[ae]n|wom[ae]n)":     {"mailman", "mailwoman", "mailmen", "mailwomen"},
		"middle(?:m[ae]n|wom[ae]n)":   {"middleman", "middlewoman", "middlemen", "middlewomen"},
		"news(?:m[ae]n|wom[ae]n)":     {"newsman", "newswoman", "newsmen", "newswomen"},
		"ombuds(?:man|woman)":         {"ombudsman", "ombudswoman"},
		"work(?:m[ae]n|wom[ae]n)":     {"workman", "workwoman", "workmen", "workwomen"},
		"police(?:m[ae]n|wom[ae]n)":   {"policeman", "policewoman", "policemen", "policewomen"},
		"repair(?:m[ae]n|wom[ae]n)":   {"repairman", "repairwoman", "repairmen", "repairwomen"},
		"sales(?:m[ae]n|wom[ae]n)":    {"salesman", "saleswoman", "salesmen", "saleswomen"},
		"service(?:m[ae]n|wom[ae]n)":  {"serviceman", "servicewoman", "servicemen", "servicewomen"},
		"steward(?:ess)?":             {"steward", "stewardess"},
		"tribes(?:m[ae]n|wom[ae]n)":   {"tribesman", "tribeswoman", "tribesmen", "tribeswomen"},
	}
	for re, matches := range reToMatches {
		regex := regexp.MustCompile(re)
		for _, match := range matches {
			assert.Equal(t, true, regex.MatchString(match))
		}
	}
}

func benchmarkLint(path string, b *testing.B) {
	path, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	config := core.LoadConfig()
	mgr := check.NewManager(config)
	linter := Linter{Config: config, CheckManager: mgr}
	for n := 0; n < b.N; n++ {
		_, _ = linter.Lint([]string{path}, "*")
	}
}

func BenchmarkLintRST(b *testing.B) {
	benchmarkLint("../fixtures/benchmarks/bench.rst", b)
}

func BenchmarkLintMD(b *testing.B) {
	benchmarkLint("../fixtures/benchmarks/bench.md", b)
}

func BenchmarkLintOrg(b *testing.B) {
	benchmarkLint("../fixtures/benchmarks/bench.org", b)
}
