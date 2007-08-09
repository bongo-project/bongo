## Sundial OPTIONS request method handler.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
from SundialHandler import SundialHandler

## Class to handle the OPTIONS request method.
class OptionsHandler(SundialHandler):
    ## The constructor.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def __init__(self, req, rp):
        pass

    ## Returns whether authentication is required for the method.
    #  @param self The object pointer.
    #  @param rp The SundialPath instance for the current request.
    def NeedsAuth(self, rp):
        return True

    ## The default entry point for the handler.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def do_OPTIONS(self, req, rp):
        # Total lies. At the time of writing, Allow should be more like "OPTIONS, PROPFIND (barely), REPORT (barely)".
        # It's getting there though!
        req.headers_out['Allow'] = "OPTIONS, GET, HEAD, POST, PUT, DELETE, TRACE, COPY, MOVE, PROPFIND, PROPPATCH, LOCK, UNLCOK, REPORT, ACL"
        req.headers_out['DAV'] = "1, 2, access-control, calendar-access"
        req.headers_out['Content-Length'] = "0"
        return bongo.commonweb.HTTP_OK
