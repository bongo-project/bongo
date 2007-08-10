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
        self.store = StoreClient(req.user, rp.user, authPassword=req.password)

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
        # rp.filename should be something like:
        # 00000000000000115.ics
        filename_parts = rp.filename.split('.')

        # Any filename with more than one "." can get lost, and this shop only
        # serves up iCalendar.
        if len(filename_parts) != 2 or filename_parts[-1] != 'ics':
            return bongo.commonweb.HTTP_NOT_FOUND

        # Get the nmap document from the store.
        # A CommandError exception is thrown when the item does not exist.
        try:
            doc = self.store.Read(filename_parts[0])
        except CommandError:
            return bongo.commonweb.HTTP_NOT_FOUND

        jsob = bongojson.ParseString(doc.strip())

        if jsob:
            data = cal.JsonToIcal(jsob)
        else:
            data = ""

        # Get rid of the boring headers, and data.
        req.headers_out["Content-Length"] = str(len(data))
        req.content_type = "text/calendar"

        req.write(data)
        return bongo.commonweb.HTTP_OK
