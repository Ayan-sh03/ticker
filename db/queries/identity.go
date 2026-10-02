package queries

import _ "embed"

//go:embed get_login_user.sql
var GetLoginUser string

//go:embed get_session.sql
var GetSession string

//go:embed create_session.sql
var CreateSession string

//go:embed delete_session.sql
var DeleteSession string
