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

const (
	dbUser     = "root"
	dbPassword = "12345678"
	dbHost     = "localhost"
	dbPort     = 3306
	dbName     = "banking-application"
)

type TransactionType string

const (
	Credit TransactionType = "Credit"
	Debit  TransactionType = "Debit"
)

type BankAccount struct {
	ID               int    `json:"id"`
	AccountHolderName string `json:"account_holder_name"`
	AccountNumber    string `json:"account_number"`
}

type Transaction struct {
	ID        int            `json:"trans_id"`
	AccountID int            `json:"account_id"`
	TransType TransactionType `json:"trans_type"`
	Amount    float64        `json:"amount"`
	TransTime time.Time      `json:"trans_time"`
}

// Response structs
type CreateAccountResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Account BankAccount `json:"account,omitempty"`
}

type SearchAccountResponse struct {
	Success  bool          `json:"success"`
	Message  string        `json:"message"`
	Accounts []BankAccount `json:"accounts,omitempty"`
}

type TransactionResponse struct {
	Success     bool        `json:"success"`
	Message     string      `json:"message"`
	Transaction Transaction `json:"transaction,omitempty"`
}

type BalanceResponse struct {
	Success      bool    `json:"success"`
	Message      string  `json:"message"`
	AccountID    int     `json:"account_id"`
	AccountNumber string `json:"account_number"`
	Balance      float64 `json:"balance"`
}

var db *sql.DB

func main() {
	var err error
	config := mysql.Config{
		User:                 dbUser,
		Passwd:               dbPassword,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%d", dbHost, dbPort),
		DBName:               dbName,
		AllowNativePasswords: true,
		ParseTime:            true,
	}
	
	db, err = sql.Open("mysql", config.FormatDSN())
	if err != nil {
		log.Fatalf("Error opening database: %v", err)
	}
	defer db.Close()

	err = db.Ping()
	if err != nil {
		log.Fatalf("Error connecting to database: %v", err)
	}
	log.Println("Successfully connected to database")
	router := mux.NewRouter()
	router.HandleFunc("/account", createAccount).Methods("POST")
	router.HandleFunc("/account/search", searchAccount).Methods("GET")
	router.HandleFunc("/transaction/deposit", createDeposit).Methods("POST")
	router.HandleFunc("/transaction/withdraw", createWithdrawal).Methods("POST")
	router.HandleFunc("/account/balance/{id}", checkBalance).Methods("GET")

	log.Println("Server starting on port 8080...")
	log.Fatal(http.ListenAndServe(":8080", router))
}

func createAccount(w http.ResponseWriter, r *http.Request) {
	var account BankAccount
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&account); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	defer r.Body.Close()

	// Insert into database
	result, err := db.Exec(
		"INSERT INTO bank_accounts (account_holder_name, account_number) VALUES (?, ?)",
		account.AccountHolderName, account.AccountNumber)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Get the last inserted ID
	lastID, err := result.LastInsertId()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	
	account.ID = int(lastID)

	respondWithJSON(w, http.StatusCreated, CreateAccountResponse{
		Success: true,
		Message: "Account created successfully",
		Account: account,
	})
}

// searchAccount searches for bank accounts based on account holder name or account number
func searchAccount(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	name := query.Get("name")
	number := query.Get("number")

	if name == "" && number == "" {
		respondWithError(w, http.StatusBadRequest, "Please provide either name or account number for search")
		return
	}

	var rows *sql.Rows
	var err error

	if name != "" && number != "" {
		// Search by both name and number
		rows, err = db.Query("SELECT id, account_holder_name, account_number FROM bank_accounts WHERE account_holder_name LIKE ? AND account_number = ?",
			"%"+name+"%", number)
	} else if name != "" {
		// Search by name
		rows, err = db.Query("SELECT id, account_holder_name, account_number FROM bank_accounts WHERE account_holder_name LIKE ?",
			"%"+name+"%")
	} else {
		// Search by account number
		rows, err = db.Query("SELECT id, account_holder_name, account_number FROM bank_accounts WHERE account_number = ?",
			number)
	}

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	accounts := []BankAccount{}
	for rows.Next() {
		var a BankAccount
		if err := rows.Scan(&a.ID, &a.AccountHolderName, &a.AccountNumber); err != nil {
			respondWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		accounts = append(accounts, a)
	}

	if len(accounts) == 0 {
		respondWithJSON(w, http.StatusOK, SearchAccountResponse{
			Success: false,
			Message: "No accounts found",
		})
		return
	}

	respondWithJSON(w, http.StatusOK, SearchAccountResponse{
		Success:  true,
		Message:  fmt.Sprintf("Found %d account(s)", len(accounts)),
		Accounts: accounts,
	})
}

// createDeposit creates a credit transaction (deposit)
func createDeposit(w http.ResponseWriter, r *http.Request) {
	createTransaction(w, r, Credit)
}

// createWithdrawal creates a debit transaction (withdrawal)
func createWithdrawal(w http.ResponseWriter, r *http.Request) {
	createTransaction(w, r, Debit)
}

// createTransaction is a helper function to create transactions
func createTransaction(w http.ResponseWriter, r *http.Request, transType TransactionType) {
	var transaction Transaction
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&transaction); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}
	defer r.Body.Close()

	// Validate account exists
	var exists int
	err := db.QueryRow("SELECT COUNT(*) FROM bank_accounts WHERE id = ?", transaction.AccountID).Scan(&exists)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if exists == 0 {
		respondWithError(w, http.StatusNotFound, "Account not found")
		return
	}

	// For withdrawals, check if there's enough balance
	if transType == Debit {
		balance, err := getAccountBalance(transaction.AccountID)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if balance < transaction.Amount {
			respondWithError(w, http.StatusBadRequest, "Insufficient funds")
			return
		}
	}

	// Set transaction type and time
	transaction.TransType = transType
	transaction.TransTime = time.Now()

	// Insert transaction
	result, err := db.Exec(
		"INSERT INTO transaction (account_id, trans_type, amount, trans_time) VALUES (?, ?, ?, ?)",
		transaction.AccountID, transaction.TransType, transaction.Amount, transaction.TransTime)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Get the last inserted ID
	lastID, err := result.LastInsertId()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	
	transaction.ID = int(lastID)

	// Prepare response message
	message := "Deposit completed successfully"
	if transType == Debit {
		message = "Withdrawal completed successfully"
	}

	respondWithJSON(w, http.StatusCreated, TransactionResponse{
		Success:     true,
		Message:     message,
		Transaction: transaction,
	})
}

// checkBalance returns the current balance for an account
func checkBalance(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	id := params["id"]

	var accountID int
	var accountNumber string
	err := db.QueryRow("SELECT id, account_number FROM bank_accounts WHERE id = ?", id).Scan(&accountID, &accountNumber)
	if err != nil {
		if err == sql.ErrNoRows {
			respondWithError(w, http.StatusNotFound, "Account not found")
		} else {
			respondWithError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	balance, err := getAccountBalance(accountID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondWithJSON(w, http.StatusOK, BalanceResponse{
		Success:      true,
		Message:      "Balance retrieved successfully",
		AccountID:    accountID,
		AccountNumber: accountNumber,
		Balance:      balance,
	})
}

// getAccountBalance calculates the current balance for an account
func getAccountBalance(accountID int) (float64, error) {
	var balance float64

	// Get sum of all credits
	var creditSum sql.NullFloat64
	err := db.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM transaction WHERE account_id = ? AND trans_type = ?",
		accountID, Credit).Scan(&creditSum)
	if err != nil {
		return 0, err
	}

	// Get sum of all debits
	var debitSum sql.NullFloat64
	err = db.QueryRow("SELECT COALESCE(SUM(amount), 0) FROM transaction WHERE account_id = ? AND trans_type = ?",
		accountID, Debit).Scan(&debitSum)
	if err != nil {
		return 0, err
	}

	// Calculate balance
	if creditSum.Valid {
		balance += creditSum.Float64
	}
	if debitSum.Valid {
		balance -= debitSum.Float64
	}

	return balance, nil
}

// respondWithError returns an error message
func respondWithError(w http.ResponseWriter, code int, message string) {
	respondWithJSON(w, code, map[string]string{"success": "false", "message": message})
}

// respondWithJSON writes the response with the specified payload
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	response, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(response)
}

