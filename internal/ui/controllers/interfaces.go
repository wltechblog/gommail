// Package controllers provides implementations for UI controllers
// that manage different aspects of the main window functionality.
package controllers

import (
	"github.com/wltechblog/gommail/internal/config"
)

// AccountController provides access to the currently selected account.
// MessageListController consumes this; MainWindow uses the concrete
// AccountControllerImpl directly.
type AccountController interface {
	GetCurrentAccount() *config.Account
}

// SortBy represents the sorting criteria for messages
type SortBy int

const (
	SortByDate SortBy = iota
	SortBySender
	SortBySubject
)

// SortOrder represents the sorting order
type SortOrder int

const (
	SortAscending SortOrder = iota
	SortDescending
)
