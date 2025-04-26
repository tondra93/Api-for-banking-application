package main
import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
)

//database
const (
	dbUser="root"
	dbPassword="12345678"
	dbHost="localhost"
	dbPort=3306
	dbName="banking-application"
)
