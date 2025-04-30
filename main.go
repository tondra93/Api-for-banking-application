package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/mux"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Database connection parameters
const (
	dbUser     = "root"
	dbPassword = "12345678"
	dbHost     = "localhost"
	dbPort     = 3306
	dbName     = "bank_app"
)

// BankAccount model
type BankAccount struct {
	ID                uint          `json:"id" gorm:"primaryKey"`
	AccountHolderName string        `json:"account_holder_name" gorm:"size:255;not null"`
	AccountNumber     string        `json:"account_number" gorm:"size:50;not null;uniqueIndex"`
	Transactions      []Transaction `json:"-" gorm:"foreignKey:AccountID"`
}

// Transaction model
type Transaction struct {
	ID        uint        `json:"id" gorm:"primaryKey"`
	AccountID uint        `json:"account_id" gorm:"not null"`
	TransType string      `json:"trans_type" gorm:"type:enum('credit','debit');not null"`
	Amount    float64     `json:"amount" gorm:"type:decimal(15,2);not null"`
	TransTime time.Time   `json:"trans_time" gorm:"not null"`
	Account   BankAccount `json:"-" gorm:"foreignKey:AccountID"`
}

// CreateAccountRequest structure for account creation
type CreateAccountRequest struct {
	AccountHolderName string `json:"account_holder_name"`
	AccountNumber     string `json:"account_number"`
}

// TransactionRequest structure for transaction creation
type TransactionRequest struct {
	AccountID uint    `json:"account_id"`
	Amount    float64 `json:"amount"`
}

// Response structures
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// BalanceResponse structure
type BalanceResponse struct {
	AccountID     uint    `json:"account_id"`
	AccountNumber string  `json:"account_number"`
	AccountHolder string  `json:"account_holder"`
	Balance       float64 `json:"balance"`
}

var db *gorm.DB

func main() {
	// First create the database if it doesn't exist
	if err := createDatabaseIfNotExists(); err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}

	// Connect to the database
	var err error
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local&allowNativePasswords=true",
		dbUser, dbPassword, dbHost, dbPort, dbName)

	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Println("Connected to database successfully")

	// Auto migrate the schema
	err = db.AutoMigrate(&BankAccount{}, &Transaction{})
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
	log.Println("Database migrated successfully")

	// Router setup
	r := mux.NewRouter()

	// Define routes
	r.HandleFunc("/accounts", createAccount).Methods("POST")
	r.HandleFunc("/accounts/search", searchAccounts).Methods("GET")
	r.HandleFunc("/transactions/deposit", createDeposit).Methods("POST")
	r.HandleFunc("/transactions/withdraw", createWithdrawal).Methods("POST")
	r.HandleFunc("/accounts/{id}/balance", getBalance).Methods("GET")

	// Start server
	log.Println("Server started on port 8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}

// createDatabaseIfNotExists creates the database if it doesn't exist
func createDatabaseIfNotExists() error {
	// Connect to MySQL without specifying a database
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/", dbUser, dbPassword, dbHost, dbPort)
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("error connecting to MySQL: %v", err)
	}
	defer sqlDB.Close()

	// Create the database if it doesn't exist
	_, err = sqlDB.Exec(fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", dbName))
	if err != nil {
		return fmt.Errorf("error creating database: %v", err)
	}

	log.Printf("Database '%s' ensured", dbName)
	return nil
}

// createAccount creates a new bank account
func createAccount(w http.ResponseWriter, r *http.Request) {
	var req CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	defer r.Body.Close()

	// Validate input
	if req.AccountHolderName == "" || req.AccountNumber == "" {
		respondWithError(w, http.StatusBadRequest, "Account holder name and account number are required")
		return
	}

	// Check if account number already exists
	var count int64
	db.Model(&BankAccount{}).Where("account_number = ?", req.AccountNumber).Count(&count)
	if count > 0 {
		respondWithError(w, http.StatusConflict, "Account number already exists")
		return
	}

	// Create account
	account := BankAccount{
		AccountHolderName: req.AccountHolderName,
		AccountNumber:     req.AccountNumber,
	}

	if err := db.Create(&account).Error; err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create account: "+err.Error())
		return
	}

	respondWithJSON(w, http.StatusCreated, Response{
		Success: true,
		Message: "Account created successfully",
		Data:    account,
	})
}

// searchAccounts searches for accounts by name or account number
func searchAccounts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := query.Get("name")
	number := query.Get("number")

	if name == "" && number == "" {
		respondWithError(w, http.StatusBadRequest, "Please provide name or account number for search")
		return
	}

	var accounts []BankAccount
	dbQuery := db.Model(&BankAccount{})

	if name != "" && number != "" {
		dbQuery = dbQuery.Where("account_holder_name LIKE ? AND account_number = ?", "%"+name+"%", number)
	} else if name != "" {
		dbQuery = dbQuery.Where("account_holder_name LIKE ?", "%"+name+"%")
	} else {
		dbQuery = dbQuery.Where("account_number = ?", number)
	}

	result := dbQuery.Find(&accounts)
	if result.Error != nil {
		respondWithError(w, http.StatusInternalServerError, "Error searching accounts: "+result.Error.Error())
		return
	}

	if len(accounts) == 0 {
		respondWithJSON(w, http.StatusOK, Response{
			Success: false,
			Message: "No accounts found",
		})
		return
	}

	respondWithJSON(w, http.StatusOK, Response{
		Success: true,
		Message: fmt.Sprintf("Found %d account(s)", len(accounts)),
		Data:    accounts,
	})
}

// createTransaction handles both deposits and withdrawals
func createTransaction(w http.ResponseWriter, r *http.Request, transType string) {
	var req TransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	defer r.Body.Close()

	// Validate input
	if req.AccountID == 0 || req.Amount <= 0 {
		respondWithError(w, http.StatusBadRequest, "Valid account ID and positive amount are required")
		return
	}

	// Check if account exists
	var account BankAccount
	if err := db.First(&account, req.AccountID).Error; err != nil {
		respondWithError(w, http.StatusNotFound, "Account not found")
		return
	}

	// For withdrawals, check if sufficient balance
	if transType == "debit" {
		var balance float64
		balance, err := getAccountBalanceById(req.AccountID)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Error checking balance: "+err.Error())
			return
		}

		if balance < req.Amount {
			respondWithError(w, http.StatusBadRequest, "Insufficient funds")
			return
		}
	}

	// Create transaction
	transaction := Transaction{
		AccountID: req.AccountID,
		TransType: transType,
		Amount:    req.Amount,
		TransTime: time.Now(),
	}

	if err := db.Create(&transaction).Error; err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create transaction: "+err.Error())
		return
	}

	// Prepare response message
	message := "Deposit completed successfully"
	if transType == "debit" {
		message = "Withdrawal completed successfully"
	}

	respondWithJSON(w, http.StatusCreated, Response{
		Success: true,
		Message: message,
		Data:    transaction,
	})
}

// createDeposit handles deposit transactions
func createDeposit(w http.ResponseWriter, r *http.Request) {
	createTransaction(w, r, "credit")
}

// createWithdrawal handles withdrawal transactions
func createWithdrawal(w http.ResponseWriter, r *http.Request) {
	createTransaction(w, r, "debit")
}

// getBalance retrieves the current balance for an account
func getBalance(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	id, err := strconv.Atoi(params["id"])
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid account ID")
		return
	}

	// Check if account exists
	var account BankAccount
	if err := db.First(&account, id).Error; err != nil {
		respondWithError(w, http.StatusNotFound, "Account not found")
		return
	}

	// Calculate balance
	balance, err := getAccountBalanceById(uint(id))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error calculating balance: "+err.Error())
		return
	}

	// Prepare response
	balanceResp := BalanceResponse{
		AccountID:     account.ID,
		AccountNumber: account.AccountNumber,
		AccountHolder: account.AccountHolderName,
		Balance:       balance,
	}

	respondWithJSON(w, http.StatusOK, Response{
		Success: true,
		Message: "Balance retrieved successfully",
		Data:    balanceResp,
	})
}

// getAccountBalanceById calculates the current balance for an account
func getAccountBalanceById(accountID uint) (float64, error) {
	var creditSum, debitSum float64

	// Get sum of credits
	if err := db.Model(&Transaction{}).
		Where("account_id = ? AND trans_type = ?", accountID, "credit").
		Select("COALESCE(SUM(amount), 0)").
		Scan(&creditSum).Error; err != nil {
		return 0, err
	}

	// Get sum of debits
	if err := db.Model(&Transaction{}).
		Where("account_id = ? AND trans_type = ?", accountID, "debit").
		Select("COALESCE(SUM(amount), 0)").
		Scan(&debitSum).Error; err != nil {
		return 0, err
	}

	return creditSum - debitSum, nil
}

// respondWithError returns an error response
func respondWithError(w http.ResponseWriter, code int, message string) {
	respondWithJSON(w, code, Response{
		Success: false,
		Message: message,
	})
}

// respondWithJSON returns a JSON response
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, _ := json.Marshal(payload)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}
