## Sundial resource path class.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
from bongo.commonweb.HttpError import HttpError
import re
import cElementTree

import GetView
import PropfindView
import OptionsView
import ReportView

## Dictionary of views available depending on type of request method.
views = {
    'GET' : GetView.GetHandler,
    'PROPFIND' : PropfindView.PropfindHandler,
    'OPTIONS' : OptionsView.OptionsHandler,
    'REPORT' : ReportView.ReportHandler
}

## Resource path class for Sundial.
class SundialPath:
    ## The constructor.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    def __init__(self, req):
        self.req = req

        self.view = self.action = self.path = None

        req.log.debug(str(self))

        if req.headers_in.get('Content-Length', None):
            raw_input = str(req.read(int(req.headers_in.get('Content-Length', None))))

        # Look for XML content, and if it exists, parse it.
        # RFC2518 says that both text/xml and application/xml are valid.
        p = re.compile("(application|text)/xml")
        if p.match(str(req.headers_in.get('Content-Type', None))):
            self.xml_input = cElementTree.XML(raw_input)

        # For debugging, print out headers_in, and the content.
        print req.headers_in
        try:
            print raw_input
        except:
            pass

    ## Returns the handler for the request method.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def GetHandler(self, req, rp):
        return views[req.method](req, rp)

