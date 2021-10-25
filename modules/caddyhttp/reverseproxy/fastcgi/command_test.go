package fastcgi

import "testing"

func TestProcessPHPFastCGI(t *testing.T) {
	_, _, _, err := processPHPFastCGI("app.example.com", "localhost:9000", "/var/www/html")
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = processPHPFastCGI("app.example.com", ":9000", "/var/www/html")
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = processPHPFastCGI("http://localhost:1234", "external.example.com:9000", "/var/www/html")
	if err != nil {
		t.Fatal(err)
	}

	_, _, _, err = processPHPFastCGI("app.example.com", ":9000", "")
	if err == nil {
		t.Fatal("expected complaint about missing 'root', but did not return error")
	}

	_, _, _, err = processPHPFastCGI("app.example.com", "http://localhost", "/var/www/html")
	if err == nil {
		t.Fatal("expected complaint about invalid 'to', but did not return error")
	}
}
