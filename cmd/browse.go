package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"github.com/manifoldco/promptui"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

var pageSize int
var tableFormat bool

var browseCmd = &cobra.Command{
	Use:   "browse",
	Short: "Browse Firestore collections and documents",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		client, err := createFirestoreClient(ctx)
		if err != nil {
			log.Fatalf("Failed to create firestore client: %v", err)
		}
		defer client.Close()

		browseCollections(client, ctx)
	},
}

func init() {
	browseCmd.Flags().BoolVarP(&tableFormat, "table", "t", false, "Display data in table format")
	browseCmd.Flags().IntVarP(&pageSize, "page-size", "p", 10, "Number of documents to display per page")
	rootCmd.AddCommand(browseCmd)
}

func createFirestoreClient(ctx context.Context) (*firestore.Client, error) {
	opt := option.WithCredentialsFile("./firebase_secret.json")

	app, err := firebase.NewApp(ctx, nil, opt)
	if err != nil {
		return nil, err
	}

	return app.Firestore(ctx)
}

func browseCollections(client *firestore.Client, ctx context.Context) {
	for {
		collections, err := getAllCollections(client, ctx)
		if err != nil {
			log.Fatalf("Failed to get collections: %v", err)
		}

		prompt := promptui.Select{
			Label: "Select a collection to browse or exit",
			Items: append(collections, "Exit"),
		}

		_, collection, err := prompt.Run()
		if err != nil {
			log.Fatalf("Failed to select collection: %v", err)
		}

		if collection == "Exit" {
			fmt.Println("Goodbye!")
			return
		}

		browseDocuments(client, ctx, collection, nil)
	}
}

func getAllCollections(client *firestore.Client, ctx context.Context) ([]string, error) {
	iter := client.Collections(ctx)
	collections := []string{}

	for {
		ref, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		collections = append(collections, ref.ID)
	}

	return collections, nil
}

func browseDocuments(client *firestore.Client, ctx context.Context, collection string, searchCondition *firestore.Query) {
	displayFormatPrompt := promptui.Select{
		Label: "Select display format",
		Items: []string{"Table Format", "Column Format"},
	}

	_, displayFormat, err := displayFormatPrompt.Run()
	if err != nil {
		log.Fatalf("Failed to select display format: %v", err)
	}

	page := 0
	for {
		query := client.Collection(collection).Offset(pageSize * page).Limit(pageSize)
		if searchCondition != nil {
			query = searchCondition.Offset(pageSize * page).Limit(pageSize)
		}
		iter := query.Documents(ctx)
		docs, err := iter.GetAll()
		if err != nil {
			log.Fatalf("Failed to get documents: %v", err)
		}

		if len(docs) == 0 {
			fmt.Println("─────────────────────────────")
			fmt.Println(" No more documents available.")
			fmt.Println("─────────────────────────────")
			return
		}

		fmt.Printf("Page %d of collection '%s':\n\n", page+1, collection)
		displayDocuments(docs, displayFormat)

		prompt := promptui.Select{
			Label: "Select an action",
			Items: []string{"Next Page", "Previous Page", "New Search Condition", "Clear Search Condition", "Back to Collections"},
		}

		_, action, err := prompt.Run()
		if err != nil {
			log.Fatalf("Failed to select action: %v", err)
		}

		switch action {
		case "Next Page":
			page++
		case "Previous Page":
			if page > 0 {
				page--
			}
		case "New Search Condition":
			newSearchCondition := searchDocuments(client, ctx, collection, displayFormat)
			if newSearchCondition != nil {
				searchCondition = newSearchCondition
				page = 0
			}
		case "Clear Search Condition":
			searchCondition = nil
			page = 0
		case "Back to Collections":
			return
		}
	}
}

func searchDocuments(client *firestore.Client, ctx context.Context, collection string, displayFormat string) *firestore.Query {
	fieldPrompt := promptui.Prompt{
		Label: "Field",
		Validate: func(input string) error {
			if input == "" {
				return errors.New("Field name cannot be empty")
			}
			return nil
		},
	}
	ops := []string{"==", "<", "<=", ">", ">=", "array-contains", "in", "array-contains-any"}
	operatorPrompt := promptui.Select{
		Label: "Operator",
		Items: ops,
	}
	queryPrompt := promptui.Prompt{
		Label: "Query",
	}

	fieldName, err := fieldPrompt.Run()
	if err != nil {
		log.Fatalf("Prompt failed: %v", err)
	}
	operatorIndex, operator, err := operatorPrompt.Run()
	if err != nil {
		log.Fatalf("Prompt failed: %v", err)
	}
	queryValue, err := queryPrompt.Run()
	if err != nil {
		log.Fatalf("Prompt failed: %v", err)
	}

	// Convert the input value to the appropriate type for the given operator.
	var value interface{} = queryValue
	if operatorIndex >= 1 && operatorIndex <= 4 { // <, <=, >, >=
		if floatValue, err := strconv.ParseFloat(queryValue, 64); err == nil {
			value = floatValue
		} else if intValue, err := strconv.ParseInt(queryValue, 10, 64); err == nil {
			value = intValue
		}
	} else if operatorIndex == 6 || operatorIndex == 7 { // in, array-contains-any
		valueAsSlice := strings.Split(queryValue, ",")
		value = splitToChunks(valueAsSlice, 10)
	}

	query := client.Collection(collection).Where(fieldName, operator, value).Limit(10)

	iter := query.Documents(ctx)
	docs, err := iter.GetAll()
	if err != nil {
		log.Fatalf("Failed to get documents: %v", err)
	}

	if len(docs) == 0 {
		fmt.Println("─────────────────────────────")
		fmt.Println(" No documents found.")
		fmt.Println("─────────────────────────────")
		time.Sleep(2 * time.Second)
		return nil
	}

	fmt.Println("/////////////Preview/////////////////")
	fmt.Println("検索条件:")
	fmt.Printf("  Field: %s\n", fieldName)
	fmt.Printf("  Operator: %s\n", operator)
	fmt.Printf("  Value: %v\n", queryValue)
	displayDocuments(docs, displayFormat)
	fmt.Println("/////////////Preview/////////////////")

	prompt := promptui.Select{
		Label: "Select an action",
		Items: []string{"Apply Search Condition", "Modify Search Condition", "Back to Documents"},
	}

	_, action, err := prompt.Run()
	if err != nil {
		log.Fatalf("Failed to select action: %v", err)
	}

	switch action {
	case "Apply Search Condition":
		return &query
	case "Modify Search Condition":
		return searchDocuments(client, ctx, collection, displayFormat)
	case "Back to Documents":
		return nil
	}

	return nil
}
func splitToChunks(slice []string, chunkSize int) [][]string {
	var chunks [][]string
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}
	return chunks
}

func displayDocuments(documents []*firestore.DocumentSnapshot, displayFormat string) {
	if len(documents) == 0 {
		fmt.Println("No documents found.")
		return
	}

	switch displayFormat {
	case "Table Format":
		printDocumentsTable(documents)
	case "Column Format":
		printDocumentsKeyValue(documents)
	}
}

func displayDocument(doc *firestore.DocumentSnapshot) {
	if tableFormat {
		printDocumentTable(doc)
	} else {
		printDocumentKeyValue(doc)
	}
}

func printDocumentsTable(documents []*firestore.DocumentSnapshot) {
	if len(documents) == 0 {
		fmt.Println("No documents found.")
		return
	}

	headers := make([]string, 0)
	for k := range documents[0].Data() {
		headers = append(headers, k)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader(headers)
	table.SetBorder(false)
	table.SetRowSeparator("-")
	table.SetCenterSeparator("|")
	table.SetAutoFormatHeaders(true)
	table.SetAutoWrapText(false)
	table.SetAutoMergeCells(false)
	table.SetRowLine(true)
	table.SetColumnSeparator(" ")

	for _, doc := range documents {
		row := make([]string, 0)
		data := doc.Data()
		for _, header := range headers {
			value := fmt.Sprintf("%v", data[header])
			row = append(row, value)
		}
		table.Append(row)
	}

	table.Render()
}

func printDocumentTable(doc *firestore.DocumentSnapshot) {
	data := doc.Data()

	headers := make([]string, 0)
	for k := range data {
		headers = append(headers, k)
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader(headers)
	table.SetBorder(false)
	table.SetRowSeparator("-")
	table.SetCenterSeparator("|")
	table.SetAutoFormatHeaders(true)
	table.SetAutoWrapText(false)
	table.SetAutoMergeCells(false)
	table.SetRowLine(true)
	table.SetColumnSeparator(" ")

	row := make([]string, 0)
	for _, header := range headers {
		value := fmt.Sprintf("%v", data[header])
		row = append(row, value)
	}
	table.Append(row)

	table.Render()
}

func printDocumentsKeyValue(documents []*firestore.DocumentSnapshot) {
	if len(documents) == 0 {
		fmt.Println("No documents found.")
		return
	}

	for _, doc := range documents {
		fmt.Printf("\nDocument ID: %s\n", doc.Ref.ID)
		data := doc.Data()
		for k, v := range data {
			fmt.Printf("  %s: %v\n", k, v)
		}
	}
}

func printDocumentKeyValue(doc *firestore.DocumentSnapshot) {
	fmt.Printf("\nDocument ID: %s\n", doc.Ref.ID)
	data := doc.Data()
	for k, v := range data {
		fmt.Printf("  %s: %v\n", k, v)
	}
}
