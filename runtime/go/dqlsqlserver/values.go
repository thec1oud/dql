package dqlsqlserver

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	mssql "github.com/microsoft/go-mssqldb"

	"github.com/thec1oud/dql/runtime/go/civil"
	"github.com/thec1oud/dql/runtime/go/dec"
)

// decode turns one value the driver scanned, from a column of the named
// database type, into its dql.Row form. The driver gives integers as
// int64, floats as float64, bit as bool, text and xml as string, and the
// date and time types as time.Time, a time of day on 0001-01-01 and a
// datetimeoffset in its offset; a decimal, money or uniqueidentifier comes
// as bytes, a uniqueidentifier's in SQL Server's mixed-endian order.
func decode(typ string, v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case time.Time:
		switch typ {
		case "DATE":
			return civil.DateOf(x), nil
		case "TIME":
			return civil.TimeOf(x), nil
		case "DATETIMEOFFSET":
			return civil.OffsetDateTimeOf(x), nil
		}
		// DATETIME2, DATETIME and SMALLDATETIME hold no time zone.
		return civil.DateTimeOf(x), nil
	case []byte:
		return decodeBytes(typ, x)
	case string:
		if typ == "JSON" {
			return json.RawMessage(x), nil
		}
	}
	return v, nil
}

func decodeBytes(typ string, b []byte) (any, error) {
	switch typ {
	case "DECIMAL", "MONEY", "SMALLMONEY":
		d, err := dec.Parse(string(b))
		if err != nil {
			return nil, fmt.Errorf("dqlsqlserver: %s value %q: %w", typ, b, err)
		}
		return d, nil
	case "UNIQUEIDENTIFIER":
		var u mssql.UniqueIdentifier
		if err := u.Scan(b); err != nil {
			return nil, fmt.Errorf("dqlsqlserver: %s value %x: %w", typ, b, err)
		}
		return uuid.UUID(u), nil
	case "JSON":
		return json.RawMessage(b), nil
	}
	return b, nil
}
