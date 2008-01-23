## Sundial resource path class.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
from bongo.commonweb.HttpError import HttpError
import re
import lxml.etree as et
import urllib

import GetView
import PropfindView
import OptionsView
import ReportView
import DeleteView
import PutView

## Dictionary of views available depending on type of request method.
views = {
    'GET' : GetView.GetHandler,
    'PROPFIND' : PropfindView.PropfindHandler,
    'OPTIONS' : OptionsView.OptionsHandler,
    'REPORT' : ReportView.ReportHandler,
    'DELETE' : DeleteView.DeleteHandler,
    'PUT' : PutView.PutHandler
}

## Resource path class for Sundial.
class SundialPath(object):

    ## Takes the request URI and parses it into its separate parts.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    def _handle_path(self, req):
        # There's no law to say that the "calendars/" directory is at /calendars/.
        # So, let's create the right path *not including* the actual
        # calendars directory. E.g. if the request was for /something/calendars/blah,
        # the sundialpath will be "/something".
        splituri = req.uri.split('/')
        pathparts = []
        path = splituri[:]
        for i in path:
            splituri.__delitem__(0)
            # SundialUriRoot comes in as "/calendars" but i
            # will not have the following slash, so the [1:]
            # is just to remove that.
            if i == req.get_options()['SundialUriRoot'][1:]:
                break
            pathparts.append(i)

        self.sundialpath = '/'.join(pathparts)

        if len(splituri) != 3:
            raise HttpError(404)

        # The user the calendar belongs to.
        self.user = splituri[0]

        # The calendar name.
        self.calendar = urllib.unquote(splituri[1])

        # The file name.
        self.filename = splituri[2]

        if self.filename != '':
            # This shop only serves up iCalendar.
            if self.filename[-4:] != '.ics':
                raise HttpError(404)

            self.filename = self.filename[:-4]
        else:
            self.filename = None

    ## The constructor.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    def __init__(self, req):
        self.req = req

        self.view = self.action = self.sundialpath = self.user = self.calender = self.filename = None

        req.log.debug(str(self))

        # The path should be something like one of the following:
        # {/otherpath}/calendars/user/calendar/
        # {/calendars}/user/calendar/filename.ics
        self._handle_path(req)

        if req.headers_in.get('Content-Length', None):
            self.raw_input = str(req.read(int(req.headers_in.get('Content-Length', None))))

        # Look for XML content, and if it exists, parse it.
        # RFC2518 says that both text/xml and application/xml are valid.
        p = re.compile("(application|text)/xml")
        if p.match(str(req.headers_in.get('Content-Type', None))):
            self.xml_input = et.XML(self.raw_input)

        # For debugging, print out headers_in, and the content.
        #print req.headers_in
        #try:
        #    print self.raw_input
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
        user_agent = self.req.headers_in.get('User-Agent')

        if user_agent is not None and user_agent.startswith('Evolution'):
            # TODO This doesn't support https.
            return 'http://' + self.req.headers_in.get('Host') + self.sundialpath

        return self.sundialpath
