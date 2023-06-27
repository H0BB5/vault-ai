package form

import "github.com/h0bb5/vault-ai/errorlist"

type Form interface {
	Validate() errorlist.Errors
	String() string
}
