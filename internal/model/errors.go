package model

import "errors"

var (
	ErrNotIndexed         = errors.New("project not indexed, run 'fcg index .' first")
	ErrStorageUnavailable = errors.New("storage backend unavailable")
	ErrLockHeld           = errors.New("index lock held by another user")
	ErrBranchNotFound     = errors.New("branch not found in index")
	ErrUnsupportedLang    = errors.New("unsupported language")
)
