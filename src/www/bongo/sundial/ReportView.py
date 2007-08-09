## Sundial REPORT request method handler.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
from bongo.commonweb.ElementTree import normalize
from SundialHandler import SundialHandler
from bongo.store.StoreClient import StoreClient
import cElementTree as ET
import md5
from bongo.external.dateutil.parser import parse
from libbongo.libs import cal, bongojson

## Class to handle the PROPFIND request method.
class ReportHandler(SundialHandler):
    ## The constructor.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def __init__(self, req, rp):
        self.store = StoreClient(req.user, req.user, authPassword=req.get_basic_auth_pw())

    ## Returns whether authentication is required for the method.
    #  @param self The object pointer.
    #  @param rp The SundialPath instance for the current request.
    def NeedsAuth(self, rp):
        return True

    ## Returns hostname based on HTTP_HOST.
    #  @param self The object pointer.
    #
    # Evolution does not support relative URLs in dav::href, therefore
    # This function returns to ideal form of value prefix. Defaulting
    # to relative, but using definite when Evolution is the User-Agent.
    def _get_hostname(self):
        # There's no law to say that the "dav/" directory is at /dav/.
        # So, create the right path *without* the actual dav directory.
        path = []
        for i in self.req.uri.split('/'):
            if i == 'dav':
                break
            path += i

        if self.req.headers_in.get('User-Agent').startswith('Evolution'):
            # TODO This doesn't support https.
            return 'http://' + self.req.headers_in.get('Host') + '/'.join(path)
        else:
            return '/'.join(path)

    ## Creates appropriate XML tags for requested VEVENTS.
    #  @param self The object pointer.
    #  @param output Dictionary of some information needed for the request.
    #  @param multistatus_tag The <D:multistatus> tag so SubElements can be created.
    #
    # First, events are pulled from the store based on the collection/calendar and also
    # the filters used. Then, <D:response> tags are created, satisfying all the <D:prop>
    # requests, one-by-one.
    def _vevent_filter(self, output, prop_tags, multistatus_tag):
        try:
            start = output['start']
            end = output['end']
        except KeyError:
            start = None
            end = None

        # Grab 'dem events from the store.
        # TODO Update this to not use "all" calendar.
        events = list(self.store.Events(None, ["nmap.document", "nmap.event.calendars"], start=start, end=end))

        # Loop through each VEVENT that was returned.
        for response in events:
            response_tag = ET.SubElement(multistatus_tag, 'D:response') # <D:response>
            # TODO Update this URL scheme.
            ET.SubElement(response_tag, 'D:href').text = self._get_hostname() + "dav/%s.ics" % response.uid # <D:href>url</D:href>

            print self._get_hostname()

            propstat_tag = ET.SubElement(response_tag, 'D:propstat') # <D:propstat>
            prop_tag = ET.SubElement(propstat_tag, 'D:prop') # <D:prop>

            # For each VEVENT, loop through each prop.
            for prop in prop_tags:
                # Get NMAP document for current VEVENT.
                doc = response.props['nmap.document'].strip()

                # Parse the document into JSON.
                jsob = bongojson.ParseString(doc)

                # Convert JSON to iCalendar format.
                calendardata = cal.JsonToIcal(jsob)

                # Deal with each tag.
                if prop == 'dav::getetag':
                    ET.SubElement(prop_tag, 'D:getetag').text = '"%s"' % md5.new(calendardata).hexdigest() # <D:getetag>etag</D:getetag>
                elif prop == 'urn:ietf:params:xml:ns:caldav:calendar-data':
                    ET.SubElement(prop_tag, 'C:calendar-data').text = calendardata # <C:calendar-data>BEGIN:VCALENDAR...</C:calendar-data>
                # TODO Implement more of these.

            ET.SubElement(propstat_tag, 'D:status').text = "HTTP/1.1 200 OK"

            # Nothing to return, as the D:multistatus tag instance pointer has been messed with.

    ## Handles the D:calendar-query request.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def calendar_query(self, req, rp):
        self.req = req
        output = {}

        # Loop each child tag of D:calendar-query.
        for t in rp.xml_input.getchildren():
            if normalize(t.tag) == 'dav::prop':
                # List of /normalize/d props, required.
                prop_tags = [normalize(prop.tag) for prop in t.getchildren()]

            elif normalize(t.tag) == 'urn:ietf:params:xml:ns:caldav:filter':
                # There is only one C:comp-filter element under C:filter.
                vcalendar = t.getchildren()[0]
                # It should have name="VCALENDAR".
                if vcalendar.get('name') != 'VCALENDAR':
                    continue

                # There can also be only one C:comp-filter element under the previous.
                # It's going to have name="%s", where %s = VEVENT, or VTODO..
                # TODO Perhaps more here..
                filter = vcalendar.getchildren()[0]

                if filter.get('name') == 'VEVENT':
                    print 'OH HAI'
                    output['type'] = 'VEVENT'

                    # Loop through different ranges and whatnot.
                    for f in filter.getchildren():
                        if normalize(f.tag) == 'urn:ietf:params:xml:ns:caldav:time-range':
                            # C:time-range -- only events between two times.
                            output['start'] = parse(f.get('start'))
                            output['end'] = parse(f.get('end'))

                        elif normalize(f.tag) == 'urn:ietf:params:xml:ns:caldav:is-defined':
                            # This means nothing as all events in the store are "defined".
                            pass

                elif filter.get('name') == 'VTODO':
                    output['type'] = 'VTODO'
                    # TODO Implement this, perhaps.
                    pass

        # Generate <D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
        multistatus_tag = ET.Element('D:multistatus') 
        bongo.commonweb.ElementTree.set_prefixes(multistatus_tag, {'D' : 'DAV:',
                                                                    'C' : 'urn:ietf:params:xml:ns:caldav'
                                                                })

        if output['type'] == 'VEVENT':
            self._vevent_filter(output, prop_tags, multistatus_tag)

        # Throw out the output.
        req.content_type = 'text/xml; charset="utf-8"'
        req.write(ET.tostring(multistatus_tag))
        return bongo.commonweb.HTTP_MULTI_STATUS

    ## The default entry point for the handler.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def do_REPORT(self, req, rp):
        return {
            'urn:ietf:params:xml:ns:caldav:calendar-query' : self.calendar_query
        }[normalize(rp.xml_input.tag)](req, rp)
