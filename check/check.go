package check

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ValeLint/vale/core"
	"github.com/ValeLint/vale/rule"
	"github.com/jdkato/prose/transform"
	"github.com/mitchellh/mapstructure"
	"gopkg.in/yaml.v2"
	"matloob.io/regexp"
)

const (
	ignoreCase      = `(?i)`
	wordTemplate    = `\b(?:%s)\b`
	nonwordTemplate = `(?:%s)`
)

type ruleFn func(string, *core.File) []core.Alert

// Manager controls the loading and validating of the check extension points.
type Manager struct {
	AllChecks map[string]Check
	Config    *core.Config
}

// NewManager creates a new Manager and loads the rule definitions (that is,
// extended checks) specified by config.
func NewManager(config *core.Config) *Manager {
	var style, path string

	mgr := Manager{AllChecks: make(map[string]Check), Config: config}

	// loadedStyles keeps track of the styles we've loaded as we go.
	loadedStyles := []string{}

	// First we load Vale's built-in rules.
	mgr.loadDefaultRules()
	if mgr.Config.StylesPath == "" {
		// If we're not given a StylesPath, there's nothing left to look for.
		return &mgr
	}

	loadedStyles = append(loadedStyles, "vale")
	baseDir := mgr.Config.StylesPath
	for _, style = range mgr.Config.GBaseStyles {
		if style == "vale" {
			// We've already loaded this style.
			continue
		}
		// Now we load all styles specified at the global ("*") level.
		mgr.loadExternalStyle(filepath.Join(baseDir, style))
		loadedStyles = append(loadedStyles, style)
	}

	for _, styles := range mgr.Config.SBaseStyles {
		for _, style := range styles {
			if !core.StringInSlice(style, loadedStyles) {
				// Now we load all styles specified at a syntax level
				//(e.g., "*.md"), assuming we didn't already load it at the
				// global level.
				mgr.loadExternalStyle(filepath.Join(baseDir, style))
				loadedStyles = append(loadedStyles, style)
			}
		}
	}

	for _, chk := range mgr.Config.Checks {
		// Finally, we load any remaining individual rules.
		if !strings.Contains(chk, ".") {
			// A rule must be associated with a style (i.e., "Style[.]Rule").
			continue
		}
		parts := strings.Split(chk, ".")
		if !core.StringInSlice(parts[0], loadedStyles) {
			// If this rule isn't part of an already-loaded style, we load it
			// individually.
			fName := parts[1] + ".yml"
			path = filepath.Join(baseDir, parts[0], fName)
			core.CheckError(mgr.loadCheck(fName, path), core.RuleError)
		}
	}

	return &mgr
}

func formatMessages(msg string, desc string, subs ...string) (string, string) {
	return core.FormatMessage(msg, subs...), core.FormatMessage(desc, subs...)
}

func makeAlert(chk Definition, loc []int, txt string) core.Alert {
	a := core.Alert{Check: chk.Name, Severity: chk.Level, Span: loc, Link: chk.Link}
	a.Message, a.Description = formatMessages(chk.Message, chk.Description,
		txt[loc[0]:loc[1]])
	return a
}

func checkConditional(txt string, chk Conditional, f *core.File, r []*regexp.Regexp) []core.Alert {
	alerts := []core.Alert{}

	// We first look for the consequent of the conditional statement.
	// For example, if we're ensuring that abbriviations have been defined
	// parenthetically, we'd have something like:
	//     "WHO" [antecedent], "World Health Organization (WHO)" [consequent]
	// In other words: if "WHO" exists, it must also have a definition -- which
	// we're currently looking for.
	matches := r[0].FindAllStringSubmatch(txt, -1)
	for _, mat := range matches {
		if len(mat) > 1 {
			// If we find one, we store it in a slice associated with this
			// particular file.
			f.Sequences = append(f.Sequences, mat[1])
		}
	}

	// Now we look for the antecedent.
	locs := r[1].FindAllStringIndex(txt, -1)
	if locs != nil {
		for _, loc := range locs {
			s := txt[loc[0]:loc[1]]
			if !core.StringInSlice(s, f.Sequences) && !core.StringInSlice(s, chk.Exceptions) {
				// If we've found one (e.g., "WHO") and we haven't marked it as
				// being defined previously, send an Alert.
				alerts = append(alerts, makeAlert(chk.Definition, loc, txt))
			}
		}
	}

	return alerts
}

func checkExistence(txt string, chk Existence, f *core.File, r *regexp.Regexp) []core.Alert {
	alerts := []core.Alert{}
	locs := r.FindAllStringIndex(txt, -1)
	if locs != nil {
		for _, loc := range locs {
			alerts = append(alerts, makeAlert(chk.Definition, loc, txt))
		}
	}
	return alerts
}

func checkOccurrence(txt string, chk Occurrence, f *core.File, r *regexp.Regexp, lim int) []core.Alert {
	var loc []int

	alerts := []core.Alert{}
	locs := r.FindAllStringIndex(txt, -1)
	occurrences := len(locs)
	if occurrences > lim {
		loc = []int{locs[0][0], locs[occurrences-1][1]}
		a := core.Alert{Check: chk.Name, Severity: chk.Level, Span: loc,
			Link: chk.Link}
		a.Message = chk.Message
		a.Description = chk.Description
		alerts = append(alerts, a)
	}

	return alerts
}

func checkRepetition(txt string, chk Repetition, f *core.File, r *regexp.Regexp) []core.Alert {
	var curr, prev string
	var hit bool
	var ploc []int
	var count int

	alerts := []core.Alert{}
	for _, loc := range r.FindAllStringIndex(txt, -1) {
		curr = strings.TrimSpace(txt[loc[0]:loc[1]])
		if chk.Ignorecase {
			hit = strings.ToLower(curr) == strings.ToLower(prev) && curr != ""
		} else {
			hit = curr == prev && curr != ""
		}
		if hit {
			count++
		}
		if hit && count > chk.Max {
			floc := []int{ploc[0], loc[1]}
			a := core.Alert{Check: chk.Name, Severity: chk.Level, Span: floc,
				Link: chk.Link}
			a.Message, a.Description = formatMessages(chk.Message,
				chk.Description, curr)
			alerts = append(alerts, a)
			count = 0
		}
		ploc = loc
		prev = curr
	}
	return alerts
}

func checkSubstitution(txt string, chk Substitution, f *core.File, r *regexp.Regexp, repl []string) []core.Alert {
	alerts := []core.Alert{}
	pos := false

	// Leave early if we can to avoid calling `FindAllStringSubmatchIndex`
	// unnecessarily.
	if !r.MatchString(txt) {
		return alerts
	}

	for _, submat := range r.FindAllStringSubmatchIndex(txt, -1) {
		for idx, mat := range submat {
			if mat != -1 && idx > 0 && idx%2 == 0 {
				loc := []int{mat, submat[idx+1]}
				// Based on the current capture group (`idx`), we can determine
				// the associated replacement string by using the `repl` slice:
				expected := repl[(idx/2)-1]
				observed := strings.TrimSpace(txt[loc[0]:loc[1]])
				if expected != observed {
					if chk.POS != "" {
						// If we're given a POS pattern, check that it matches.
						//
						// If it doesn't match, the alert doesn't get added to
						// a File (i.e., `hide` == true).
						pos = core.CheckPOS(loc, chk.POS, txt)
					}
					a := core.Alert{
						Check: chk.Name, Severity: chk.Level, Span: loc,
						Link: chk.Link, Hide: pos}
					a.Message, a.Description = formatMessages(chk.Message,
						chk.Description, expected, observed)
					alerts = append(alerts, a)
				}
			}
		}
	}

	return alerts
}

func checkConsistency(txt string, chk Consistency, f *core.File, r *regexp.Regexp, opts []string) []core.Alert {
	alerts := []core.Alert{}
	loc := []int{}

	matches := r.FindAllStringSubmatchIndex(txt, -1)
	for _, submat := range matches {
		for idx, mat := range submat {
			if mat != -1 && idx > 0 && idx%2 == 0 {
				loc = []int{mat, submat[idx+1]}
				f.Sequences = append(f.Sequences, r.SubexpNames()[idx/2])
			}
		}
	}

	if matches != nil && core.AllStringsInSlice(opts, f.Sequences) {
		chk.Name = chk.Extends
		alerts = append(alerts, makeAlert(chk.Definition, loc, txt))
	}
	return alerts
}

func checkCapitalization(txt string, chk Capitalization, f *core.File) []core.Alert {
	alerts := []core.Alert{}
	if !chk.Check(txt) {
		alerts = append(alerts, makeAlert(chk.Definition, []int{0, len(txt)}, txt))
	}
	return alerts
}

func (mgr *Manager) addCapitalizationCheck(chkName string, chkDef Capitalization) {
	if chkDef.Match == "$title" {
		var tc *transform.TitleConverter
		if chkDef.Style == "Chicago" {
			tc = transform.NewTitleConverter(transform.ChicagoStyle)
		} else {
			tc = transform.NewTitleConverter(transform.APStyle)
		}
		chkDef.Check = func(s string) bool { return title(s, tc) }
	} else if f, ok := varToFunc[chkDef.Match]; ok {
		chkDef.Check = f
	} else {
		re, err := regexp.Compile(chkDef.Match)
		if !core.CheckError(err, core.RegexError) {
			return
		}
		chkDef.Check = re.MatchString
	}
	fn := func(text string, file *core.File) []core.Alert {
		return checkCapitalization(text, chkDef, file)
	}
	mgr.updateAllChecks(chkDef.Definition, fn)
}

func (mgr *Manager) addConsistencyCheck(chkName string, chkDef Consistency) {
	var chkRE string

	regex := ""
	if chkDef.Ignorecase {
		regex += ignoreCase
	}
	if !chkDef.Nonword {
		regex += wordTemplate
	} else {
		regex += nonwordTemplate
	}

	count := 0
	chkKey := strings.Split(chkName, ".")[1]
	for v1, v2 := range chkDef.Either {
		count += 2
		subs := []string{
			fmt.Sprintf("%s%d", chkKey, count), fmt.Sprintf("%s%d", chkKey, count+1)}

		chkRE = fmt.Sprintf("(?P<%s>%s)|(?P<%s>%s)", subs[0], v1, subs[1], v2)
		chkRE = fmt.Sprintf(regex, chkRE)
		re, err := regexp.Compile(chkRE)
		if core.CheckError(err, core.RegexError) {
			chkDef.Extends = chkName
			chkDef.Name = fmt.Sprintf("%s.%s", chkName, v1)
			fn := func(text string, file *core.File) []core.Alert {
				return checkConsistency(text, chkDef, file, re, subs)
			}
			mgr.updateAllChecks(chkDef.Definition, fn)
		}
	}
}

func (mgr *Manager) addExistenceCheck(chkName string, chkDef Existence) {
	regex := ""
	if chkDef.Ignorecase {
		regex += ignoreCase
	}

	regex += strings.Join(chkDef.Raw, "")
	if !chkDef.Nonword && len(chkDef.Tokens) > 0 {
		regex += wordTemplate
	} else {
		regex += nonwordTemplate
	}

	regex = fmt.Sprintf(regex, strings.Join(chkDef.Tokens, "|"))
	re, err := regexp.Compile(regex)
	if core.CheckError(err, core.RegexError) {
		fn := func(text string, file *core.File) []core.Alert {
			return checkExistence(text, chkDef, file, re)
		}
		mgr.updateAllChecks(chkDef.Definition, fn)
	}
}

func (mgr *Manager) addRepetitionCheck(chkName string, chkDef Repetition) {
	regex := ""
	if chkDef.Ignorecase {
		regex += ignoreCase
	}
	regex += `(` + strings.Join(chkDef.Tokens, "|") + `)`
	re, err := regexp.Compile(regex)
	if core.CheckError(err, core.RegexError) {
		fn := func(text string, file *core.File) []core.Alert {
			return checkRepetition(text, chkDef, file, re)
		}
		mgr.updateAllChecks(chkDef.Definition, fn)
	}
}

func (mgr *Manager) addOccurrenceCheck(chkName string, chkDef Occurrence) {
	re, err := regexp.Compile(chkDef.Token)
	if core.CheckError(err, core.RegexError) && chkDef.Max >= 1 {
		fn := func(text string, file *core.File) []core.Alert {
			return checkOccurrence(text, chkDef, file, re, chkDef.Max)
		}
		mgr.updateAllChecks(chkDef.Definition, fn)
	}
}

func (mgr *Manager) addConditionalCheck(chkName string, chkDef Conditional) {
	var re *regexp.Regexp
	var expression []*regexp.Regexp
	var err error

	re, err = regexp.Compile(chkDef.Second)
	if !core.CheckError(err, core.RegexError) {
		return
	}
	expression = append(expression, re)

	re, err = regexp.Compile(chkDef.First)
	if !core.CheckError(err, core.RegexError) {
		return
	}
	expression = append(expression, re)

	fn := func(text string, file *core.File) []core.Alert {
		return checkConditional(text, chkDef, file, expression)
	}
	mgr.updateAllChecks(chkDef.Definition, fn)
}

func (mgr *Manager) addSubstitutionCheck(chkName string, chkDef Substitution) {
	regex := ""
	tokens := ""

	if chkDef.Ignorecase {
		regex += ignoreCase
	}

	if !chkDef.Nonword {
		regex += wordTemplate
	} else {
		regex += nonwordTemplate
	}

	replacements := []string{}
	for regexstr, replacement := range chkDef.Swap {
		opens := strings.Count(regexstr, "(")
		if opens != strings.Count(regexstr, "?:") &&
			opens != strings.Count(regexstr, `\(`) {
			// We rely on manually-added capture groups to associate a match
			// with its replacement -- e.g.,
			//
			//    `(foo)|(bar)`, [replacement1, replacement2]
			//
			// where the first capture group ("foo") corresponds to the first
			// element of the replacements slice ("replacement1"). This means
			// that we can only accept non-capture groups from the user (the
			// indexing would be mixed up otherwise).
			//
			// TODO: Should we change this? Perhaps by creating a map of regex
			// to replacements?
			continue
		}
		tokens += `(` + regexstr + `)|`
		replacements = append(replacements, replacement)
	}

	regex = fmt.Sprintf(regex, strings.TrimRight(tokens, "|"))
	re, err := regexp.Compile(regex)
	if core.CheckError(err, core.RegexError) {
		fn := func(text string, file *core.File) []core.Alert {
			return checkSubstitution(text, chkDef, file, re, replacements)
		}
		mgr.updateAllChecks(chkDef.Definition, fn)
	}
}

func (mgr *Manager) updateAllChecks(chkDef Definition, fn ruleFn) {
	chk := Check{Rule: fn, Extends: chkDef.Extends, Code: chkDef.Code}
	chk.Level = core.LevelToInt[chkDef.Level]
	chk.Scope = core.Selector{Value: chkDef.Scope}
	mgr.AllChecks[chkDef.Name] = chk
}

func (mgr *Manager) makeCheck(generic map[string]interface{}, extends, chkName string) {
	// TODO: make this less ugly ...
	if extends == "existence" {
		def := Existence{}
		if err := mapstructure.Decode(generic, &def); err == nil {
			mgr.addExistenceCheck(chkName, def)
		}
	} else if extends == "substitution" {
		def := Substitution{}
		if err := mapstructure.Decode(generic, &def); err == nil {
			mgr.addSubstitutionCheck(chkName, def)
		}
	} else if extends == "occurrence" {
		def := Occurrence{}
		if err := mapstructure.Decode(generic, &def); err == nil {
			mgr.addOccurrenceCheck(chkName, def)
		}
	} else if extends == "repetition" {
		def := Repetition{}
		if err := mapstructure.Decode(generic, &def); err == nil {
			mgr.addRepetitionCheck(chkName, def)
		}
	} else if extends == "consistency" {
		def := Consistency{}
		if err := mapstructure.Decode(generic, &def); err == nil {
			mgr.addConsistencyCheck(chkName, def)
		}
	} else if extends == "conditional" {
		def := Conditional{}
		if err := mapstructure.Decode(generic, &def); err == nil {
			mgr.addConditionalCheck(chkName, def)
		}
	} else if extends == "capitalization" {
		def := Capitalization{}
		if err := mapstructure.Decode(generic, &def); err == nil {
			mgr.addCapitalizationCheck(chkName, def)
		}
	}
}

func validateDefinition(generic map[string]interface{}, name string) error {
	msg := name + ": %s!"
	if point, ok := generic["extends"]; !ok {
		return fmt.Errorf(msg, "missing extension point")
	} else if !core.StringInSlice(point.(string), extensionPoints) {
		return fmt.Errorf(msg, "unknown extension point")
	} else if _, ok := generic["message"]; !ok {
		return fmt.Errorf(msg, "missing message")
	}
	return nil
}

func (mgr *Manager) addCheck(file []byte, chkName string) error {
	// Load the rule definition.
	generic := map[string]interface{}{}
	err := yaml.Unmarshal(file, &generic)
	if err != nil {
		return fmt.Errorf("%s: %s", chkName, err.Error())
	} else if defErr := validateDefinition(generic, chkName); defErr != nil {
		return defErr
	}

	// Set default values, if necessary.
	generic["name"] = chkName
	if level, ok := mgr.Config.RuleToLevel[chkName]; ok {
		generic["level"] = level
	} else if _, ok := generic["level"]; !ok {
		generic["level"] = "warning"
	}
	if _, ok := generic["scope"]; !ok {
		generic["scope"] = "text"
	}

	mgr.makeCheck(generic, generic["extends"].(string), chkName)
	return nil
}

func (mgr *Manager) loadExternalStyle(path string) {
	err := filepath.Walk(path,
		func(fp string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			core.CheckError(mgr.loadCheck(fi.Name(), fp), core.StyleError)
			return nil
		})
	core.CheckError(err, core.StyleError)
}

func (mgr *Manager) loadCheck(fName string, fp string) error {
	if strings.HasSuffix(fName, ".yml") {
		f, err := ioutil.ReadFile(fp)
		if !core.CheckError(err, core.RuleError) {
			return err
		}

		style := filepath.Base(filepath.Dir(fp))
		chkName := style + "." + strings.Split(fName, ".")[0]
		if _, ok := mgr.AllChecks[chkName]; ok {
			return fmt.Errorf("(%s): duplicate check", chkName)
		}
		return mgr.addCheck(f, chkName)
	}
	return nil
}

func (mgr *Manager) loadDefaultRules() {
	for _, chk := range defaultRules {
		b, err := rule.Asset("rule/" + chk + ".yml")
		if err != nil {
			continue
		}
		core.CheckError(mgr.addCheck(b, "vale."+chk), core.BinError)
	}
}
