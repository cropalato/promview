package auth

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidViewGroup marks a view group or pin the caller can fix by sending a
// different one, as opposed to a failure that is ours.
var ErrInvalidViewGroup = errors.New("invalid view group")

// ErrViewGroupNotFound is returned when a named view group does not exist.
var ErrViewGroupNotFound = errors.New("view group not found")

// ErrViewGroupPinned is returned when a user tries to leave a group an
// administrator pinned on them.
var ErrViewGroupPinned = errors.New("view group is pinned and cannot be left")

// ViewGroup is an administrator-defined selector users can join to narrow their
// default view. It is never a permission: scope decides what a user may read.
type ViewGroup struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Matchers    []LabelMatcher `json:"matchers,omitempty"`
}

// ViewGroupPin forces a view group on a user, or on every member of a directory
// group. It mirrors the subject shape of a RoleBinding.
type ViewGroupPin struct {
	SubjectKind   string `json:"subjectKind"`
	UserID        int64  `json:"userID,omitempty"`
	SubjectIssuer string `json:"subjectIssuer,omitempty"`
	SubjectGroup  string `json:"subjectGroup,omitempty"`
}

// ViewGroupStatus is a view group as one caller sees it. Matchers, Pins and
// Members are filled in only for administrators: a selector can name teams or
// labels the reader has no access to.
type ViewGroupStatus struct {
	ViewGroup
	Joined  bool           `json:"joined"`
	Pinned  bool           `json:"pinned"`
	Pins    []ViewGroupPin `json:"pins,omitempty"`
	Members int            `json:"members,omitempty"`
}

func ValidateViewGroup(group ViewGroup) error {
	if err := validateViewGroup(group); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidViewGroup, err)
	}
	return nil
}

func validateViewGroup(group ViewGroup) error {
	if !bindingNamePattern.MatchString(group.Name) {
		return errors.New("view group name must use lowercase letters, digits, underscores, or hyphens")
	}
	if len(group.Description) > 512 {
		return errors.New("description must not exceed 512 characters")
	}
	if len(group.Matchers) == 0 {
		return errors.New("view group requires at least one selector")
	}
	if len(group.Matchers) > 16 {
		return errors.New("view group must not contain more than 16 selectors")
	}
	for _, matcher := range group.Matchers {
		if !labelNamePattern.MatchString(matcher.Name) {
			return errors.New("selector label name is invalid")
		}
		if matcher.Value == "" || len(matcher.Value) > 256 {
			return errors.New("selector value is required and must not exceed 256 characters")
		}
		supported := false
		for _, operator := range supportedMatcherOps {
			supported = supported || matcher.Operator == operator
		}
		if !supported {
			return errors.New("selector must use =, !=, =~, or !~")
		}
		if matcher.Operator == "=~" || matcher.Operator == "!~" {
			if _, err := regexp.Compile(matcher.Value); err != nil {
				return fmt.Errorf("selector regular expression is invalid: %w", err)
			}
		}
	}
	return nil
}

// ValidateViewGroupPin reuses the role binding subject rules so the two cannot
// drift apart.
func ValidateViewGroupPin(pin ViewGroupPin) error {
	err := validateRoleBinding(RoleBinding{
		Name: "pin", Role: RoleViewer, SubjectKind: pin.SubjectKind,
		UserID: pin.UserID, SubjectIssuer: pin.SubjectIssuer, SubjectGroup: pin.SubjectGroup,
	})
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidViewGroup, err)
	}
	return nil
}
