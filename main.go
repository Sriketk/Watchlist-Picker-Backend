package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

//Film struct for http response
type film struct {
	Slug  string `json:"slug"`      //url of film
	Image string `json:"image_url"` //url of image
	Year  string `json:"release_year"`
	Name  string `json:"film_name"`
}

//struct for channel to send film and whether is has finshed a user
type filmSend struct {
	film film //film to be sent over channel
	done bool //if user is done
}

type nothingReason int

const (
	INTERSECT = iota
	UNION
)

type nothingError struct {
	reason nothingReason
}

func (e *nothingError) ToString() string {
	switch e.reason {
	case INTERSECT:
		return "empty intersect"
	case UNION:
		return "empty union"
	default:
		return "big error"
	}

}

func (e *nothingError) Error() string {
	return e.ToString()
}

const url = "https://letterboxd.com/ajax/poster" //first part of url for getting full info on film
const urlEnd = "menu/linked/125x187/"            // second part of url for getting full info on film
const site = "https://letterboxd.com"

func main() {
	getFilmHandler := http.HandlerFunc(getFilm)
	http.Handle("/film", getFilmHandler)
	log.Println("serving at :8080")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Listening on port %s", port)
	http.ListenAndServe(":"+port, nil)
}

var year int

func init() {
	year = time.Now().Year()
}

func getFilm(w http.ResponseWriter, r *http.Request) {
	enableCors(&w)
	log.Println(year)
	query := r.URL.Query() //Get URL Params(type map)
	users, ok := query["users"]
	log.Println(len(users))
	if !ok || len(users) == 0 {
		http.Error(w, "no users", 400)
		return
	}
	_, inter := query["intersect"]
	_, ignore := query["ignore_unreleased"]

	var userFilm film
	var err error
	if ignore {
		if inter {
			if len(users) == 1 {
				userFilm, err = scrapeUser(users, false, true)
			} else {
				userFilm, err = scrapeUser(users, true, true)
			}
		} else {
			userFilm, err = scrapeUser(users, false, true)
		}
	} else {
		if inter {
			if len(users) == 1 {
				userFilm, err = scrapeUser(users, false, false)
			} else {
				userFilm, err = scrapeUser(users, true, false)
			}
		} else {
			userFilm, err = scrapeUser(users, false, false)
		}
	}
	if err != nil {
		var e *nothingError
		if errors.As(err, &e) {
			switch e.reason {
			case INTERSECT:
				http.Error(w, "Intersect error", 406)
				return
			case UNION:
				http.Error(w, "Union error", 404)
				return
			}
		}
	}

	js, err := json.Marshal(userFilm)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.Write(js)
}

//main scraping function
func scrapeUser(users []string, intersect bool, ignore bool) (film, error) {
	var user int = 0          //conuter for number of users increses by one when a users page starts being scraped decreses when user has finished think kinda like a semaphore
	var totalFilms []film     //final list to hold all film
	ch := make(chan filmSend) //channel to send films over
	// start go routine to scrape each user
	for _, a := range users {
		log.Println(a)
		user++
		if strings.Contains(a, "/") {
			go scrapeList(a, ch)
		} else {
			go scrape(a, ch)
		}
	}
	for {
		userFilm := <-ch
		if userFilm.done { //if users channel is don't then the scapre for that user has finished so decrease the user count
			log.Printf("Received done signal, remaining users: %d", user-1)
			user--
			if user == 0 {
				break
			}
		} else {
			log.Printf("Received film: %s", userFilm.film.Name)
			totalFilms = append(totalFilms, userFilm.film) //append feilm recieved over channel to list
		}

	}

	//chose random film from list
	log.Printf("Total films collected: %d", len(totalFilms))
	if len(totalFilms) == 0 {
		log.Printf("ERROR: No films found in totalFilms")
		return film{}, &nothingError{reason: UNION}
	}
	log.Print("results")
	var finalFilm film
	if intersect {
		intersectList := getintersect(totalFilms,len(users))
		length := len(intersectList)
		if length == 0 {
			return film{}, &nothingError{reason: INTERSECT}
		}
		if ignore {
			fmt.Println("ignore")
			intersectList = removeCurrentYear(intersectList)
			length = len(intersectList)
		}
		rand.Seed(time.Now().UTC().UnixNano())
		n := rand.Intn(length)
		log.Println(length)
		log.Println(n)
		log.Println(intersectList[n])
		finalFilm = intersectList[n]
	} else {
		rand.Seed(time.Now().UTC().UnixNano())
		if ignore {
			fmt.Println("ignore")
			totalFilms = removeCurrentYear(totalFilms)
		}
		n := rand.Intn(len(totalFilms))
		log.Println(len(totalFilms))
		log.Println(n)
		log.Println(totalFilms[n])
		finalFilm = totalFilms[n]
	}
	return finalFilm, nil
}

//function to scapre an single user
func scrape(userName string, ch chan filmSend) {
	siteToVisit := site + "/" + userName + "/watchlist"
	
	log.Printf("Starting to scrape: %s", siteToVisit)

	c := colly.NewCollector(
		colly.Async(true),
	)
	c.Limit(&colly.LimitRule{DomainGlob: "*", Parallelism: 100})
	
	// Extract film info directly from main page
	c.OnHTML("div.film-poster", func(e *colly.HTMLElement) {
		log.Printf("Found film poster element")
		name := e.ChildAttr("img", "alt")  // Film name is in img alt attribute
		slug := e.Attr("data-target-link") // Target link is available
		img := e.ChildAttr("img", "src")   // Image (though it's placeholder)
		
		log.Printf("Extracted film: name=%s, slug=%s", name, slug)
		
		if name != "" && slug != "" {
			tempfilm := film{
				Slug:  (site + slug),
				Image: makeBigger(img),
				Year:  "", // Year not available on main page
				Name:  name,
			}
			ch <- ok(tempfilm)
		}
	})
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		if strings.Contains(link, "watchlist/page") {
			log.Printf("Found pagination link: %s", link)
			e.Request.Visit(e.Request.AbsoluteURL(link))
		}
	})

	c.OnRequest(func(r *colly.Request) {
		log.Printf("Visiting: %s", r.URL.String())
	})

	c.OnError(func(r *colly.Response, err error) {
		log.Printf("Error occurred: %s", err.Error())
	})

	c.Visit(siteToVisit)
	c.Wait()
	log.Printf("Finished scraping user: %s", userName)
	ch <- done() // users has finished so send done through channel

}

func scrapeList(listnameIn string, ch chan filmSend) {
	siteToVisit := ""
	listname := strings.ToLower(listnameIn)

	if strings.Contains(listname, "/list/") {
		siteToVisit = site + "/" + listname
	} else {
		strslice := strings.Split(listname, "/") //strslice[0] is user name strslice[1] is listname
		siteToVisit = site + "/" + strslice[0] + "/list/" + strslice[1]

	}
	log.Println(siteToVisit)

	c := colly.NewCollector(
		colly.Async(true),
	)
	c.Limit(&colly.LimitRule{DomainGlob: "*", Parallelism: 100})
	
	// Extract film info directly from main page
	c.OnHTML("div.film-poster", func(e *colly.HTMLElement) {
		name := e.ChildAttr("img", "alt")  // Film name is in img alt attribute
		slug := e.Attr("data-target-link") // Target link is available
		img := e.ChildAttr("img", "src")   // Image (though it's placeholder)
		
		if name != "" && slug != "" {
			tempfilm := film{
				Slug:  (site + slug),
				Image: makeBigger(img),
				Year:  "", // Year not available on main page
				Name:  name,
			}
			ch <- ok(tempfilm)
		}
	})
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		if strings.Contains(link, "/page") {
			e.Request.Visit(e.Request.AbsoluteURL(link))
		}
	})

	c.Visit(siteToVisit)
	c.Wait()
	ch <- done()

}

func ok(f film) filmSend {
	return filmSend{
		film: f,
		done: false,
	}
}

func done() filmSend {
	return filmSend{
		film: film{},
		done: true,
	}
}

func getintersect(filmSlice []film, numOfUsers int) []film {
	keys := make(map[film]int)
	list := []film{}
	for _, entry := range filmSlice {
		i, _ := keys[entry]
		if i < (numOfUsers - 1) {
			keys[entry] ++
		} else {
			list = append(list, entry)
		}
	}
	return list
}

func enableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
}

func makeBigger(url string) string {
	return strings.ReplaceAll(url, "-0-125-0-187-", "-0-230-0-345-")
}

func removeCurrentYear(filmSlice []film) []film {
	list := []film{}
	for _, entry := range filmSlice {
		if entry.Year == "" {
			continue
		}
		filmYear, _ := strconv.Atoi(entry.Year)
		if filmYear < year {
			list = append(list, entry)
		}
	}
	return list
}

