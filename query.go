// Copyright 2015 Qiang Xue. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dbx

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Params represents a list of parameter values to be bound to a SQL statement.
// The map keys are the parameter names while the map values are the corresponding parameter values.
type Params map[string]interface{}

// Executor prepares, executes, or queries a SQL statement.
type Executor interface {
	// Exec executes a SQL statement
	Exec(query string, args ...interface{}) (sql.Result, error)
	// Query queries a SQL statement
	Query(query string, args ...interface{}) (*sql.Rows, error)
	// Prepare creates a prepared statement
	Prepare(query string) (*sql.Stmt, error)
}

// Query represents a SQL statement to be executed.
type Query struct {
	executor Executor

	sql, rawSQL  string
	placeholders []string
	params       Params

	stmt *sql.Stmt

	// FieldMapper maps struct field names to DB column names.
	FieldMapper FieldMapFunc
	// LastError contains the last error (if any) of the query.
	LastError error
	// LogFunc is used to log the SQL statement being executed.
	LogFunc LogFunc
}

// NewQuery creates a new Query with the given SQL statement.
func NewQuery(db *DB, executor Executor, sql string) *Query {
	rawSQL, placeholders := db.processSQL(sql)
	return &Query{
		executor:     executor,
		sql:          sql,
		rawSQL:       rawSQL,
		placeholders: placeholders,
		params:       Params{},
		FieldMapper:  db.FieldMapper,
		LogFunc:      db.LogFunc,
	}
}

// SQL returns the original SQL used to create the query.
// The actual SQL (RawSQL) being executed is obtained by replacing the named
// parameter placeholders with anonymous ones.
func (q *Query) SQL() string {
	return q.sql
}

// logSQL returns the SQL statement with parameters being replaced with the actual values.
// The result is only for logging purpose and should not be used to execute.
func (q *Query) logSQL() string {
	s := q.sql
	for k, v := range q.params {
		var sv string
		if _, ok := v.(string); ok {
			sv = "'" + strings.Replace(v.(string), "'", "''", -1) + "'"
		} else if _, ok := v.([]byte); ok {
			sv = "'" + strings.Replace(string(v.([]byte)), "'", "''", -1) + "'"
		} else {
			sv = fmt.Sprintf("%v", v)
		}
		s = strings.Replace(s, "{:"+k+"}", sv, -1)
	}
	return s
}

// log logs a message for the currently executed SQL statement.
func (q *Query) log(start time.Time, execute bool) {
	t := float64(time.Now().Sub(start).Nanoseconds()) / 1e6
	if execute {
		q.LogFunc("[%.2fms] Execute SQL: %v", t, q.logSQL())
	} else {
		q.LogFunc("[%.2fms] Query SQL: %v", t, q.logSQL())
	}
}

// Params returns the parameters to be bound to the SQL statement represented by this query.
func (q *Query) Params() Params {
	return q.params
}

// Prepare creates a prepared statement for later queries or executions.
// Close() should be called after finishing all queries.
func (q *Query) Prepare() *Query {
	stmt, err := q.executor.Prepare(q.rawSQL)
	if err != nil {
		q.LastError = err
		return q
	}
	q.stmt = stmt
	return q
}

// Close closes the underlying prepared statement.
// Close does nothing if the query has not been prepared before.
func (q *Query) Close() error {
	if q.stmt == nil {
		return nil
	}

	err := q.stmt.Close()
	q.stmt = nil
	return err
}

// Bind sets the parameters that should be bound to the SQL statement.
// The parameter placeholders in the SQL statement are in the format of "{:ParamName}".
func (q *Query) Bind(params Params) *Query {
	if len(q.params) == 0 {
		q.params = params
	} else {
		for k, v := range params {
			q.params[k] = v
		}
	}
	return q
}

// Execute executes the SQL statement without retrieving data.
func (q *Query) Execute() (sql.Result, error) {
	if q.LastError != nil {
		return nil, q.LastError
	}

	params, err := replacePlaceholders(q.placeholders, q.params)
	if err != nil {
		q.LastError = err
		return nil, err
	}

	if q.LogFunc != nil {
		defer q.log(time.Now(), true)
	}

	var result sql.Result
	if q.stmt == nil {
		result, q.LastError = q.executor.Exec(q.rawSQL, params...)
	} else {
		result, q.LastError = q.stmt.Exec(params...)
	}

	return result, q.LastError
}

// One executes the SQL statement and populates the first row of the result into a struct or NullStringMap.
// Refer to Rows.ScanStruct() and Rows.ScanMap() for more details on how to specify
// the variable to be populated.
func (q *Query) One(a interface{}) error {
	if q.LastError != nil {
		return q.LastError
	}

	rows, err := q.Rows()
	if err != nil {
		q.LastError = err
	} else {
		q.LastError = rows.one(a)
	}
	return q.LastError
}

// One executes the SQL statement and populates all the resulting rows into a slice of struct or NullStringMap.
// The slice must be given as a pointer. The slice elements must be either structs or NullStringMap.
// Refer to Rows.ScanStruct() and Rows.ScanMap() for more details on how each slice element can be.
func (q *Query) All(slice interface{}) error {
	if q.LastError != nil {
		return q.LastError
	}

	rows, err := q.Rows()
	if err != nil {
		q.LastError = err
	} else {
		q.LastError = rows.all(slice)
	}
	return q.LastError
}

// Row executes the SQL statement and populates the first row of the result into a list of variables.
// Note that the number of the variables should match to that of the columns in the query result.
func (q *Query) Row(a ...interface{}) error {
	if q.LastError != nil {
		return q.LastError
	}

	rows, err := q.Rows()
	if err != nil {
		q.LastError = err
	} else {
		q.LastError = rows.row(a...)
	}
	return q.LastError
}

// Rows executes the SQL statement and returns a Rows object to allow retrieving data row by row.
func (q *Query) Rows() (*Rows, error) {
	if q.LastError != nil {
		return nil, q.LastError
	}

	params, err := replacePlaceholders(q.placeholders, q.params)
	if err != nil {
		q.LastError = err
		return nil, err
	}

	if q.LogFunc != nil {
		defer q.log(time.Now(), false)
	}

	var rows *sql.Rows
	if q.stmt == nil {
		rows, q.LastError = q.executor.Query(q.rawSQL, params...)
	} else {
		rows, q.LastError = q.stmt.Query(params...)
	}

	return &Rows{rows, q.FieldMapper}, q.LastError
}

// replacePlaceholders converts a list of named parameters into a list of anonymous parameters.
func replacePlaceholders(placeholders []string, params Params) ([]interface{}, error) {
	if len(placeholders) == 0 {
		return nil, nil
	}

	result := make([]interface{}, 0)
	for _, name := range placeholders {
		if value, ok := params[name]; ok {
			result = append(result, value)
		} else {
			return nil, errors.New("Named parameter not found: " + name)
		}
	}
	return result, nil
}
