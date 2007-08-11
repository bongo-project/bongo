## Sundial resource path class.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
from bongo.commonweb.HttpError import HttpError
import re
import cElementTree
import urllib

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

    ## Takes the request URI and parses it into its separate parts.
    #  @param self The object pointer.
    #  @param uri The request URI, req.uri.
    def _handle_path(self, uri):
        # There's no law to say that the "dav/" directory is at /dav/.
        # So, let's create the right path *not including* the actual
        # dav directory. E.g. if the request was for /something/dav/blah,
        # the davpath will be "/something".
        splituri = uri.split('/')
        davparts = []
        davpath = splituri[:]
        for i in davpath:
            splituri.__delitem__(0)
            if i == 'dav':
                break
            davparts.append(i)

        self.davpath = '/' + '/'.join(davparts)

        if len(splituri) != 3:
            raise HttpError(404)

        # The user the calendar belongs to.
        self.user = splituri[0]

        # The calendar name.
        self.calendar = urllib.unquote(splituri[1])

        # The file name (only used in GET at the time of writing).
        self.filename = splituri[2]

    ## The constructor.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    def __init__(self, req):
        self.req = req

        self.view = self.action = self.davpath = self.user = self.calender = self.filename = None

        req.log.debug(str(self))

        # The path should be something like one of the following:
        # {/otherpath}/dav/user/calendar/
        # {/dav}/user/calendar/longnumber.ics
        self._handle_path(req.uri)

        if req.headers_in.get('Content-Length', None):
            raw_input = str(req.read(int(req.headers_in.get('Content-Length', None))))

        # Look for XML content, and if it exists, parse it.
        # RFC2518 says that both text/xml and application/xml are valid.
        p = re.compile("(application|text)/xml")
        if p.match(str(req.headers_in.get('Content-Type', None))):
            self.xml_input = cElementTree.XML(raw_input)

        # For debugging, print out headers_in, and the content.
        #print req.headers_in
        #try:
        #    print raw_input
        #except:
        #    pass

    ## Returns the handler for the request method.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def GetHandler(self, req, rp):
        return views[req.method](req, rp)

    ## Returns hostname based on HTTP_HOST.
    #  @param self The object pointer.
    #
    # Evolution does not support relative URLs in dav::href, therefore
    def get_hostname(self):
        if self.req.headers_in.get('User-Agent').startswith('Evolution'):
            # TODO This doesn't support https.
            return 'http://' + self.req.headers_in.get('Host') + self.davpath
        else:
            return self.davpath
