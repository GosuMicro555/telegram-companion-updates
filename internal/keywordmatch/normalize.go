package keywordmatch

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type word struct {
	surface string
	morph   string
}

func tokenizeSentences(text string) [][]word {
	text = norm.NFKC.String(text)
	var result [][]word
	var sentence []word
	var token strings.Builder
	flushToken := func() {
		if token.Len() == 0 {
			return
		}
		value := normalizeSurface(token.String())
		token.Reset()
		if value != "" {
			sentence = append(sentence, word{surface: value, morph: morphologyKey(value)})
		}
	}
	flushSentence := func() {
		flushToken()
		if len(sentence) > 0 {
			result = append(result, sentence)
		}
		sentence = nil
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			token.WriteRune(r)
			continue
		}
		flushToken()
		switch r {
		case '.', '!', '?', '\n', '\r', '…':
			flushSentence()
		}
	}
	flushSentence()
	return result
}

func normalizeSurface(value string) string {
	value = strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
	value = strings.ReplaceAll(value, "ё", "е")
	return value
}

func morphologyKey(value string) string {
	switch script(value) {
	case "ru":
		return russianStem(value)
	case "en":
		return englishStem(value)
	default:
		return value
	}
}

func script(value string) string {
	current := ""
	for _, r := range value {
		next := ""
		switch {
		case unicode.In(r, unicode.Cyrillic):
			next = "ru"
		case unicode.In(r, unicode.Latin):
			next = "en"
		case unicode.IsDigit(r):
			continue
		default:
			return ""
		}
		if current != "" && current != next {
			return ""
		}
		current = next
	}
	return current
}

var russianIrregular = map[string]string{
	"деньги": "деньг", "денег": "деньг", "деньгам": "деньг", "деньгами": "деньг",
	"деньгах": "деньг", "денежка": "деньг", "денежки": "деньг", "денежек": "деньг",
	"денюжка": "деньг", "денюжки": "деньг", "деньжата": "деньг", "деньжат": "деньг",
	"бабки": "бабк", "бабок": "бабк", "бабкам": "бабк", "бабками": "бабк",
	"купить": "куп", "купил": "куп", "купила": "куп", "купили": "куп", "куплю": "куп",
	"купишь": "куп", "купит": "куп", "купим": "куп", "купят": "куп",
	"ехать": "ех", "еду": "ех", "едешь": "ех", "едет": "ех", "едем": "ех", "едут": "ех",
	"ехал": "ех", "ехала": "ех", "ехали": "ех",
}

func russianStem(value string) string {
	if stem, ok := russianIrregular[value]; ok {
		return stem
	}
	runes := []rune(value)
	for _, suffix := range russianSuffixes {
		suffixRunes := []rune(suffix)
		if len(runes)-len(suffixRunes) < 3 || !strings.HasSuffix(value, suffix) {
			continue
		}
		return string(runes[:len(runes)-len(suffixRunes)])
	}
	return value
}

var russianSuffixes = []string{
	"иями", "ями", "ами", "иями", "его", "ого", "ему", "ому", "ими", "ыми",
	"ившись", "ывшись", "ивши", "ывши", "ировать", "ировать", "овать", "евать",
	"ение", "ания", "ение", "ость", "остей", "аться", "яться", "иться",
	"ую", "юю", "ая", "яя", "ою", "ею", "ые", "ие", "ый", "ий", "ой",
	"ам", "ям", "ах", "ях", "ов", "ев", "ом", "ем", "ей", "ию", "ью",
	"ила", "ыла", "ена", "ейте", "уйте", "ите", "или", "ыли", "ей", "уй",
	"ил", "ыл", "им", "ым", "ен", "ило", "ыло", "ено", "ят", "ует", "уют",
	"ит", "ыт", "ены", "ить", "ыть", "ишь", "ую", "ю", "ла", "ете",
	"йте", "ли", "й", "л", "ем", "н", "ло", "но", "ет", "ть", "ешь",
	"нно", "ся", "сь", "а", "я", "ы", "и", "ь", "й", "у", "ю", "е", "о",
}

var englishIrregular = map[string]string{
	"bought": "buy", "buying": "buy", "buys": "buy",
	"ran": "run", "running": "run",
	"went": "go", "gone": "go",
	"cars": "car", "children": "child", "people": "person",
}

func englishStem(value string) string {
	if stem, ok := englishIrregular[value]; ok {
		return stem
	}
	runes := []rune(value)
	for _, suffix := range []string{"ization", "ational", "fulness", "ousness", "iveness", "ingly", "edly", "ments", "ment", "ness", "ation", "izer", "ing", "ied", "ies", "ed", "es", "s"} {
		suffixRunes := []rune(suffix)
		if len(runes)-len(suffixRunes) < 3 || !strings.HasSuffix(value, suffix) {
			continue
		}
		base := string(runes[:len(runes)-len(suffixRunes)])
		switch suffix {
		case "ied", "ies":
			base += "y"
		case "ing", "ed", "ingly", "edly":
			baseRunes := []rune(base)
			if len(baseRunes) > 2 && baseRunes[len(baseRunes)-1] == baseRunes[len(baseRunes)-2] {
				base = string(baseRunes[:len(baseRunes)-1])
			}
		}
		return base
	}
	return value
}

var serviceWords = func() map[string]struct{} {
	const russian = `
		а без будто бы в ведь во вокруг да даже для до если же за зато и из или к как
		когда ко кроме ли либо между на над не ни но о об обо около однако от перед по
		под потому поскольку при про пусть ради с словно со среди также то тоже у хотя
		через что чтобы

		я меня мне мной мною ты тебя тебе тобой тобою он его ему им него нему ним нем
		она ее ей ею нее ней нею оно мы нас нам нами вы вас вам вами они их ими них
		себя себе собой собою

		мой моя мое мои моего моей моему моим мою моими моих
		твой твоя твое твои твоего твоей твоему твоим твою твоими твоих
		наш наша наше наши нашего нашей нашему нашим нашу нашими наших
		ваш ваша ваше ваши вашего вашей вашему вашим вашу вашими ваших
		свой своя свое свои своего своей своему своим свою своими своих

		это этот эта эти этого этой этому этим эту этими этих
		тот та то те того той тому тем ту теми тех
		кто кого кому кем ком что чего чему чем
		какой какая какое какие какого какой какому каким какую какими каких
		который которая которое которые которого которой которому которым которую
		которыми которых чей чья чье чьи чьего чьей чьему чьим чью чьими чьих
		весь вся все всего всей всему всем всю всеми всех
		никто ничто никакой некто нечто кто-то что-то
	`
	const english = `
		a an the about above across after against along among around as at before behind
		below beneath beside between beyond by despite down during except for from in
		inside into near of off on onto out outside over past since through throughout
		to toward under until up upon with within without

		and because but if nor once or so than that though unless when whenever where
		whereas wherever whether while yet not

		i me my mine myself you your yours yourself yourselves he him his himself she
		her hers herself it its itself we us our ours ourselves they them their theirs
		themselves this these those who whom whose which what someone somebody something
		anyone anybody anything everyone everybody everything none nobody nothing each
		either neither both few many several all any some such

		am is are was were be been being do does did have has had can could may might
		must shall should will would
	`

	result := make(map[string]struct{})
	for _, value := range strings.Fields(russian + " " + english) {
		result[value] = struct{}{}
	}
	return result
}()

func isServiceWord(value string) bool {
	_, ok := serviceWords[value]
	return ok
}
