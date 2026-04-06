module github.com/rkujawa/uiscsi-tape-dd

go 1.25

// Development replace directives — remove before publishing.
replace github.com/rkujawa/uiscsi => ../../uiscsi-repo

replace github.com/rkujawa/uiscsi-tape => ../

require (
	github.com/rkujawa/uiscsi v1.1.2
	github.com/rkujawa/uiscsi-tape v0.0.0-00010101000000-000000000000
)
