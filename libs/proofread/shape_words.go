package proofread

// shapeFunctionWords are the 70 function words of Mosteller and Wallace,
// Inference and Disputed Authorship: The Federalist (1964), then the English
// closed-class pronouns, determiners, prepositions, conjunctions and
// auxiliaries their list leaves out.
var shapeFunctionWords = []string{
	"a", "all", "also", "an", "and", "any", "are", "as", "at", "be", "been", "but", "by", "can", "do", "down",
	"even", "every", "for", "from", "had", "has", "have", "her", "his", "if", "in", "into", "is", "it", "its",
	"may", "more", "must", "my", "no", "not", "now", "of", "on", "one", "only", "or", "our", "shall", "should",
	"so", "some", "such", "than", "that", "the", "their", "then", "there", "things", "this", "to", "up", "upon",
	"was", "were", "what", "when", "which", "who", "will", "with", "would", "your",

	"i", "me", "mine", "myself", "we", "us", "ours", "ourselves", "you", "yours", "yourself", "he", "him",
	"himself", "she", "hers", "herself", "itself", "they", "them", "theirs", "themselves", "whom", "whose",

	"these", "those", "each", "both", "either", "neither", "few", "many", "much", "most", "other", "another",
	"several", "same", "own",

	"about", "above", "across", "after", "against", "along", "among", "around", "before", "behind", "below",
	"between", "during", "except", "off", "onto", "out", "over", "since", "through", "under", "until",
	"within", "without",

	"because", "although", "though", "unless", "whether", "while", "where", "why", "how", "nor", "yet",

	"am", "being", "did", "does", "doing", "could", "might", "cannot",

	"here", "just", "very", "too", "again", "never",
}

var isShapeFunctionWord = wordSet(shapeFunctionWords...)
