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
    def __init__(self, req, rp):
        pass

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
        store = StoreClient(req.user, rp.user, authPassword=req.get_basic_auth_pw())

        # Get the nmap document from the store.
        try:
            doc = store.Read(rp.fileuid)
        except:
            return bongo.commonweb.HTTP_NOT_FOUND

        jsob = bongojson.ParseString(doc.strip())

        if jsob:
            # TODO TODO TODO This should not be encoded into ASCII at all.
            # At the moment, this doesn't work without though.
            data = cal.JsonToIcal(jsob).encode('ascii', 'replace')
        else:
            data = ""

        # Get rid of the boring headers, and data.
        req.headers_out["Content-Length"] = str(len(data))
        req.content_type = 'text/calendar'

        req.write(data)
        return bongo.commonweb.HTTP_OK
