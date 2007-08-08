## Sundial GET request method handler.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
from SundialHandler import SundialHandler
from bongo.store.StoreClient import StoreClient
from libbongo.libs import bongojson, cal


## Class to handle the GET request method.
class GetHandler(SundialHandler):
    ## The contructor.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    #
    # Connects to the store for later.
    def __init__(self, req, rp):
        self.store = StoreClient(req.user, req.user, authPassword=req.password)

    ## Returns whether authentication is required for this handler.
    #  @param self The object pointer.
    #  @param rp The SundialPath instance for the current request.
    def NeedsAuth(self, rp):
        return True

    ## The default entry point.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def do_GET(self, req, rp):
        if req.uri == "/dav/abcd2.ics": 
            data = """BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VTIMEZONE
LAST-MODIFIED:20040110T032845Z
TZID:US/Eastern
BEGIN:DAYLIGHT
DTSTART:20000404T020000
RRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=4
TZNAME:EDT
TZOFFSETFROM:-0500
TZOFFSETTO:-0400
END:DAYLIGHT
BEGIN:STANDARD
DTSTART:20001026T020000
RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10
TZNAME:EST
TZOFFSETFROM:-0400
TZOFFSETTO:-0500
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
DTSTART;TZID=US/Eastern:20060102T120000
DURATION:PT1H
RRULE:FREQ=DAILY;COUNT=5
SUMMARY:Event #2
UID:00959BC664CA650E933C892C@example.com
END:VEVENT
BEGIN:VEVENT
DTSTART;TZID=US/Eastern:20060104T140000
DURATION:PT1H
RECURRENCE-ID;TZID=US/Eastern:20060104T120000
SUMMARY:Event #2 bis
UID:00959BC664CA650E933C892C@example.com
END:VEVENT
BEGIN:VEVENT
DTSTART;TZID=US/Eastern:20060106T140000
DURATION:PT1H
RECURRENCE-ID;TZID=US/Eastern:20060106T120000
SUMMARY:Event #2 bis bis
UID:00959BC664CA650E933C892C@example.com
END:VEVENT
END:VCALENDAR"""
        elif req.uri == "/dav/abcd3.ics":
            data = """BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Example Corp.//CalDAV Client//EN
BEGIN:VTIMEZONE
LAST-MODIFIED:20040110T032845Z
TZID:US/Eastern
BEGIN:DAYLIGHT
DTSTART:20000404T020000
RRULE:FREQ=YEARLY;BYDAY=1SU;BYMONTH=4
TZNAME:EDT
TZOFFSETFROM:-0500
TZOFFSETTO:-0400
END:DAYLIGHT
BEGIN:STANDARD
DTSTART:20001026T020000
RRULE:FREQ=YEARLY;BYDAY=-1SU;BYMONTH=10
TZNAME:EST
TZOFFSETFROM:-0400
TZOFFSETTO:-0500
END:STANDARD
END:VTIMEZONE
BEGIN:VEVENT
DTSTART;TZID=US/Eastern:20060104T100000
DURATION:PT1H
SUMMARY:Event #3
UID:DC6C50A017428C5216A2F1CD@example.com
END:VEVENT
END:VCALENDAR"""

        req.headers_out["Content-Length"] = str(len(data))
        req.content_type = "text/calendar"

        req.write(data)
        return bongo.commonweb.HTTP_OK




#        start = end = None
#        eventProps = ["nmap.document", "nmap.event.calendars"]
#
#        events = list(self.store.Events(None, eventProps, query=None, start=start, end=end))
#
#        wholeJsob = None
#        for event in events:
#            doc = event.props["nmap.document"].strip()
#            jsob = bongojson.ParseString(doc)
#            if wholeJsob:
#                wholeJsob = cal.Merge(wholeJsob, jsob)
#            else:
#                wholeJsob = jsob
#
#        if wholeJsob:
#            data = cal.JsonToIcal(wholeJsob)
#        else:
#            data = ""
#
#        req.headers_out["Content-Length"] = str(len(data))
#        req.content_type = "text/calendar"
#
#        req.write(data)
#        return bongo.commonweb.HTTP_OK



