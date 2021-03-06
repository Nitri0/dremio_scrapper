package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/andybrewer/mack"
	"github.com/gen2brain/beeep"
	"github.com/go-playground/validator/v10"
	"github.com/prprprus/scheduler"
	"gopkg.in/yaml.v2"
)

const (
	baseUrl = ""
)

var (
	username = ""
	password = ""
	token    = ""
)

type App struct {
	Client         *http.Client
	ParametersFile string
	Rules          Rules
}

var validate *validator.Validate

type DremioResponse struct {
	Token                     string `json:"token"`
	UserName                  string `json:"userName"`
	FirstName                 string `json:"firstName"`
	LastName                  string `json:"lastName"`
	Expires                   int64  `json:"expires"`
	Email                     string `json:"email"`
	UserID                    string `json:"userId"`
	Admin                     bool   `json:"admin"`
	ClusterID                 string `json:"clusterId"`
	ClusterCreatedAt          int64  `json:"clusterCreatedAt"`
	ShowUserAndUserProperties bool   `json:"showUserAndUserProperties"`
	Version                   string `json:"version"`
	Permissions               struct {
		CanUploadProfiles   bool `json:"canUploadProfiles"`
		CanDownloadProfiles bool `json:"canDownloadProfiles"`
		CanEmailForSupport  bool `json:"canEmailForSupport"`
		CanChatForSupport   bool `json:"canChatForSupport"`
	} `json:"permissions"`
	UserCreatedAt int64 `json:"userCreatedAt"`
}

type Jobs struct {
	Jobs []struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		FailureInfo struct {
			Message string        `json:"message"`
			Errors  []interface{} `json:"errors"`
			Type    string        `json:"type"`
		} `json:"failureInfo"`
		User                 string   `json:"user"`
		StartTime            int64    `json:"startTime"`
		EndTime              int64    `json:"endTime"`
		Description          string   `json:"description"`
		DatasetPathList      []string `json:"datasetPathList"`
		DatasetType          string   `json:"datasetType"`
		RequestType          string   `json:"requestType"`
		Accelerated          bool     `json:"accelerated"`
		DatasetVersion       string   `json:"datasetVersion"`
		SnowflakeAccelerated bool     `json:"snowflakeAccelerated"`
		Spilled              bool     `json:"spilled"`
		OutputRecords        int      `json:"outputRecords"`
		OutputLimited        bool     `json:"outputLimited"`
		IsComplete           bool     `json:"isComplete"`
	} `json:"jobs"`
	Next string `json:"next"`
}

type Rules struct {
	Urls struct {
		Base      string `yaml:"baseUrl" validate:"required,url"`
		LoginPath string `yaml:"loginPath" validate:"required"`
		JobsPath  string `yaml:"jobsPath" validate:"required"`
	} `yaml:"urls"`
	LoginCredential struct {
		Username string `yaml:"username" validate:"required"`
		Password string `yaml:"password" validate:"required"`
	} `yaml:"loginCredential"`
	SearchParameters struct {
		UserJobs []string `yaml:"userJobs" validate:"required"`
		DataSet  []string `yaml:"dataSet" validate:"required"`
	} `yaml:"searchParameters"`
	FiltersParametersJobs struct {
		DeltaTime int `yaml:"deltaTime" validate:"min=1"`
	} `yaml:"filtersParametersJobs"`
}

func (app *App) init() {
	validate = validator.New()
	app.Rules.getRules(app.ParametersFile)
	app.Rules.validate()
}

func (app *App) login() {
	client := app.Client
	loginURL := app.Rules.Urls.Base + app.Rules.Urls.LoginPath
	data, _ := json.Marshal(map[string]string{
		"userName": app.Rules.LoginCredential.Username,
		"password": app.Rules.LoginCredential.Password,
	})
	requestBody := bytes.NewBuffer(data)
	response, err := client.Post(loginURL, "application/json", requestBody)
	if err != nil {
		log.Fatalln(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Fatalln(err)
		}
	}()

	if response.StatusCode != 200 {
		log.Fatalln("Login error. Status code: ", response.StatusCode)
	}

	var dremioResponse = new(DremioResponse)
	body, err := ioutil.ReadAll(response.Body)

	errJson := json.Unmarshal(body, &dremioResponse)
	if errJson != nil {
		log.Fatalln(err)
	}

	_, err = ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatalln(err)
	}
	if len(dremioResponse.Token) == 0 {
		log.Fatalln("Error: Invalid token")
	}

	token = dremioResponse.Token
}

func (app *App) getJobs() *Jobs {
	client := app.Client
	params, err := buildQueryParams(app.Rules.FiltersParametersJobs.DeltaTime)
	if err != nil {
		log.Fatalln(err)
	}

	projectUrl := app.Rules.Urls.Base + app.Rules.Urls.JobsPath + params
	req, err := http.NewRequest("GET", projectUrl, nil)
	if err != nil {
		log.Fatalln(err)
	}

	req.Header.Add("Authorization", "_dremio"+token)
	req.Header.Add("Content-Type", "application/json")
	response, err := client.Do(req)

	if err != nil {
		log.Fatalln("Error fetching response. ", err)
	}

	defer func() {
		if err := response.Body.Close(); err != nil {
			log.Fatalln(err)
		}
	}()

	var jobs = new(Jobs)

	body, err := ioutil.ReadAll(response.Body)

	errJson := json.Unmarshal(body, jobs)
	if errJson != nil {
		log.Fatalln(errJson)
	}

	_, err = ioutil.ReadAll(response.Body)
	if err != nil {
		log.Fatalln(err)
	}

	jobs = app.filterJobs(jobs) //jobs

	if len(jobs.Jobs) > 0 {
		messageError := buildErrorMessage(jobs)
		notifyError(messageError)
	} else {
		fmt.Print("Sin Errores en reflexiones")
	}
	return jobs
}

func (app *App) filterJobs(jobs *Jobs) *Jobs {
	var newjobs Jobs
	kpiNames := app.Rules.SearchParameters.DataSet
	for _, v := range jobs.Jobs {
		for _, item := range kpiNames {
			nameQuery := v.DatasetPathList[len(v.DatasetPathList)-1]
			matched, err := regexp.Match(item, []byte(nameQuery))

			if err != nil {
				log.Fatalln(err)
			}
			if matched == true {
				// TODO: Optimize deleting item from kpiNames
				newjobs.Jobs = append(newjobs.Jobs, v)
				break
			}
		}
	}
	return &newjobs
}

func notifyError(messageError string) {
	if runtime.GOOS == "darwin" {
		errSay := mack.Say("Alerta  ha ocurrido un error de reflexion en dremio!")
		if errSay != nil {
			log.Fatalln(errSay)
		}
		_, errAlert := mack.Alert("Alerta", "Ha ocurrido un(os) error(es):\n"+messageError, "critical")
		if errAlert != nil {
			log.Fatalln(errAlert)
		}

		errNotify := mack.Notify("Ha ocurrido un(os) error(es):\n"+messageError, "WIC", "Alerta", "Glass")
		if errNotify != nil {
			log.Fatalln(errNotify)
		}
	} else if runtime.GOOS == "windows" {
		err := beeep.Alert("Alerta", "Ha ocurrido un(os) error(es): "+messageError, "assets/warning.png") //+v.FailureInfo.Type, "assets/warning.png")
		if err != nil {
			panic(err)
		}
	}
}

func buildErrorMessage(jobs *Jobs) string {
	JobNames := ""
	for _, v := range jobs.Jobs {
		pathJob := " - " + strings.Join(v.DatasetPathList[:], "/")
		JobNames += pathJob + "\n"
	}
	return JobNames
}

func buildQueryParams(deltaTime int) (string, error) {
	from, to := deltaTimeCalculate(deltaTime)
	s := `?limit=1000&sort=st&order=DESCENDING&filter=(qt=="ACCELERATION");(jst=="FAILED");(st=gt=` + from + `;st=lt=` + to + `)`
	return s, nil
}

func deltaTimeCalculate(delta int) (string, string) {
	loc, _ := time.LoadLocation("UTC")
	now := time.Now().In(loc)
	oldTime := time.Date(now.Year(), now.Month(), now.Day(), now.Hour()-delta, now.Minute(), now.Second(), 0, time.UTC)

	nowUnix := now.UnixNano() / 1000000
	oldTimeUnix := oldTime.UnixNano() / 1000000
	return strconv.FormatInt(oldTimeUnix, 10), strconv.FormatInt(nowUnix, 10)
}

func (rules *Rules) getRules(path string) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatalln("Error read config file: %s", err)
		panic(err)
	}

	err = yaml.Unmarshal(file, &rules)
	if err != nil {
		log.Fatalln(err)
	}
}

func (rules *Rules) validate() {
	err := validate.Struct(rules)
	if err != nil {
		log.Fatalln("Error Parameter yaml validation : \n", err)
		panic(err)
	}
}

func main() {
	jar, _ := cookiejar.New(nil)
	app := App{
		Client:         &http.Client{Jar: jar},
		ParametersFile: "parameters.yaml",
	}

	s, err := scheduler.NewScheduler(1000)
	if err != nil {
		log.Fatalln(err)
	}

	if len(token) < 1 {
		app.init()
		app.login()
	}
	s.Delay().Minute(10).Do(app.getJobs())
}
