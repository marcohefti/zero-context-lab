package schema

import "strings"

const (
	ClassificationMissingPrimitive         = "missing_primitive"
	ClassificationNamingUX                 = "naming_ux"
	ClassificationOutputShape              = "output_shape"
	ClassificationAlreadyPossibleBetterWay = "already_possible_better_way"
)

func IsValidClassificationV1(s string) bool {
	switch strings.TrimSpace(s) {
	case "":
		return true
	case ClassificationMissingPrimitive,
		ClassificationNamingUX,
		ClassificationOutputShape,
		ClassificationAlreadyPossibleBetterWay:
		return true
	default:
		return false
	}
}
