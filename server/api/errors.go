package api

import "net/http"

func writeAPIError(w http.ResponseWriter, code int, err error) {
	writeAPIErrorMsg(w, code, err.Error())
}

func writeAPIErrorMsg(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
