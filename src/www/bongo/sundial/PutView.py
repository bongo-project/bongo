## Sundial PUT request method handler.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
from SundialHandler import SundialHandler
from bongo.store.StoreClient import StoreClient, DocTypes
from libbongo.libs import cal
from bongo.store.CommandStream import CommandError

## Class to handle the PUT request method.
class PutHandler(SundialHandler):
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
    def do_PUT(self, req, rp):
        store = StoreClient(req.user, rp.user, authPassword=req.get_basic_auth_pw())
        json = str(cal.IcalToJson(rp.raw_input))
        filename = "/events/" + rp.filename

        try: 
            store.Info(filename)
            # File exists, so overwrite it.
            store.Replace(filename, json)
        except CommandError:
            # File probably doesn't exist.
            try:
                store.Write("/events", DocTypes.Event, json, filename=rp.filename, link="/calendars/" + rp.calendar)
            except CommandError:
                return bongo.commonweb.HTTP_INTERNAL_SERVER_ERROR

        return bongo.commonweb.HTTP_CREATED
