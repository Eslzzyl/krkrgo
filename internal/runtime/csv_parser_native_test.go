package runtime

import "testing"

func TestCSVParserQuotedMultilineAndLineIteration(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	_, err := session.executeSource("<csv-parser-test>", `
Plugins.link("csvParser.dll");
p = new CSVParser(null, 44, "\n");
p.init("name,value\r\n\"a,b\",\"say \"\"hi\"\"\"\r\n\"multi\nline\",end\r\nlast,row");
a = p.getNextLine(); n1 = p.currentLineNumber;
b = p.getNextLine(); n2 = p.currentLineNumber;
c = p.getNextLine(); n3 = p.currentLineNumber;
d = p.getNextLine(); n4 = p.currentLineNumber;
e = p.getNextLine();
`, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.Evaluate(`[a[0],a[1],b[0],b[1],c[0],c[1],n1,n2,n3,n4,e === void].join("|")`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "name|value|a,b|say \"hi\"|multi\nline|end|1|2|3|4|1"; got.Text() != want {
		t.Fatalf("CSV records = %q, want %q", got.Text(), want)
	}
}

func TestCSVParserParseCallsTargetWithRecordNumbers(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	defer session.Close()
	_, err := session.executeSource("<csv-parser-callback-test>", `
Plugins.link("csvParser.dll");
global.rows = [];
target = new Dictionary();
target.doLine = function(fields, line) { global.rows.push([line, fields.join(":")]); };
p = new CSVParser(target, 44, "\n");
p.parse("x,y\n\"z\nq\",w");
`, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.Evaluate(`[rows[0][0],rows[0][1],rows[1][0],rows[1][1],p.currentLineNumber].join("|")`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "1|x:y|2|z\nq:w|2"; got.Text() != want {
		t.Fatalf("CSV callback rows = %q, want %q", got.Text(), want)
	}
}
