package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"github.com/jackbister/logsuck/internal/jobs"

	"github.com/jackbister/logsuck/internal/config"
	"github.com/jackbister/logsuck/internal/events"
	"github.com/jackbister/logsuck/internal/files"
	"github.com/jackbister/logsuck/internal/web"

	_ "github.com/mattn/go-sqlite3"
)

var cfg = config.Config{
	IndexedFiles: []config.IndexedFileConfig{},

	FieldExtractors: []*regexp.Regexp{
		regexp.MustCompile("(\\w+)=(\\w+)"),
		regexp.MustCompile("^(?P<_time>\\d\\d\\d\\d/\\d\\d/\\d\\d \\d\\d:\\d\\d:\\d\\d.\\d\\d\\d\\d\\d\\d)"),
	},

	SQLite: &config.SqliteConfig{
		DatabaseFile: "logsuck.db",
	},

	Web: &config.WebConfig{
		Enabled: true,
		Address: ":8080",
	},
}

type flagStringArray []string

func (i *flagStringArray) String() string {
	return fmt.Sprint(*i)
}

func (i *flagStringArray) Set(value string) error {
	*i = append(*i, value)
	return nil
}

var databaseFileFlag string
var eventDelimiterFlag string
var fieldExtractorFlags flagStringArray
var timeLayoutFlag string
var webAddrFlag string

func main() {
	flag.StringVar(&databaseFileFlag, "dbfile", "logsuck.db", "The name of the file in which logsuck will store its data. If the name ':memory:' is used, no file will be created and everything will be stored in memory.")
	flag.StringVar(&eventDelimiterFlag, "delimiter", "\n", "The delimiter between events in the log. Usually \\n.")
	flag.Var(&fieldExtractorFlags, "fieldextractor",
		"A regular expression which will be used to extract field values from events.\n"+
			"Can be given in two variants:\n"+
			"1. An expression with a single, named capture group. The name of the capture group will be used as the field name and the captured string will be used as the value.\n"+
			"2. An expression with two unnamed capture groups. The first capture group will be used as the field name and the second group as the value.\n"+
			"If a field with the name '_time' is extracted and matches the given timelayout, it will be used as the timestamp of the event. Otherwise the time the event was read will be used.\n"+
			"Multiple extractors can be specified by using the fieldextractor flag multiple times. "+
			"(defaults \"(\\w+)=(\\w+)\" and \"(?P<_time>\\d\\d\\d\\d/\\d\\d/\\d\\d \\d\\d:\\d\\d:\\d\\d.\\d\\d\\d\\d\\d\\d)\")")
	flag.StringVar(&timeLayoutFlag, "timelayout", "2006/01/02 15:04:05", "The layout of the timestamp which will be extracted in the _time field.")
	flag.StringVar(&webAddrFlag, "webaddr", ":8080", "The address on which the search GUI will be exposed.")
	flag.Parse()

	if databaseFileFlag != "" {
		cfg.SQLite.DatabaseFile = databaseFileFlag
	}
	if len(fieldExtractorFlags) > 0 {
		cfg.FieldExtractors = make([]*regexp.Regexp, len(fieldExtractorFlags))
		for i, fe := range fieldExtractorFlags {
			re, err := regexp.Compile(fe)
			if err != nil {
				log.Fatalf("failed to compile regex '%v': %v\n", fe, err)
			}
			cfg.FieldExtractors[i] = re
		}
	}
	if webAddrFlag != "" {
		cfg.Web.Address = webAddrFlag
	}

	cfg.IndexedFiles = make([]config.IndexedFileConfig, len(flag.Args()))
	for i, file := range flag.Args() {
		cfg.IndexedFiles[i] = config.IndexedFileConfig{
			Filename:       file,
			EventDelimiter: regexp.MustCompile(eventDelimiterFlag),
			ReadInterval:   1 * time.Second,
			TimeLayout:     timeLayoutFlag,
		}
	}

	commandChannels := make([]chan files.FileWatcherCommand, len(cfg.IndexedFiles))
	db, err := sql.Open("sqlite3", cfg.SQLite.DatabaseFile+"?cache=shared")
	if err != nil {
		log.Fatalln(err.Error())
	}
	db.SetMaxOpenConns(1)
	repo, err := events.SqliteRepository(db)
	if err != nil {
		log.Fatalln(err.Error())
	}
	publisher := events.BatchedRepositoryPublisher(&cfg, repo)
	jobRepo, err := jobs.SqliteRepository(db)
	if err != nil {
		log.Fatalln(err.Error())
	}

	for i, file := range cfg.IndexedFiles {
		commandChannels[i] = make(chan files.FileWatcherCommand)
		f, err := os.Open(file.Filename)
		if err != nil {
			log.Fatal(err)
		}
		fw := files.NewFileWatcher(file, commandChannels[i], publisher, f)
		log.Println("Starting FileWatcher for filename=" + file.Filename)
		go fw.Start()
	}

	log.Fatal(web.NewWeb(&cfg, repo, jobRepo).Serve())
}
