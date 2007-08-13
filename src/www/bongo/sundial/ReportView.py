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
        pass

    ## Returns whether authentication is required for the method.
    #  @param self The object pointer.
    #  @param rp The SundialPath instance for the current request.
    def NeedsAuth(self, rp):
        return True

    ## Creates appropriate XML tags for requested VEVENTS.
    #  @param self The object pointer.
    #  @param output Dictionary of some information needed for the request.
    #
    # First, events are pulled from the store based on the collection/calendar and also
    # the filters used. Then, <D:response> tags are created, satisfying all the <D:prop>
    # requests, one-by-one.
    def _vevent_filter(self, output, prop_tags):
        try:
            start = output['start']
            end = output['end']
        except KeyError:
            start = None
            end = None

        # Get calender GUID from name.
        try:
            info = self.store.Info("/calendars/" + self.rp.calendar)
            caluid = info.uid
        except:
            return bongo.commonweb.HTTP_NOT_FOUND

        # Grab 'dem events from the store.
        events = list(self.store.Events(caluid, ["nmap.document", "nmap.event.calendars"], start=start, end=end))

        # Loop through each VEVENT that was returned.
        for response in events:
            response_tag = ET.SubElement(self.multistatus_tag, 'D:response') # <D:response>
            ET.SubElement(response_tag, 'D:href').text = self.rp.get_hostname() + "dav/" + self.rp.user + "/" + self.rp.calendar + "/" + "%s.ics" % response.uid # <D:href>url</D:href>

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
                    ET.SubElement(prop_tag, 'D:getetag').text = '"%s"' % md5.new(calendardata.encode('ascii', 'replace')).hexdigest() # <D:getetag>etag</D:getetag>
                elif prop == 'urn:ietf:params:xml:ns:caldav:calendar-data':
                    ET.SubElement(prop_tag, 'C:calendar-data').text = calendardata # <C:calendar-data>BEGIN:VCALENDAR...</C:calendar-data>
                # TODO Implement more of these.

            ET.SubElement(propstat_tag, 'D:status').text = "HTTP/1.1 200 OK"

        return bongo.commonweb.HTTP_OK

    ## Handles the D:calendar-query request.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def calendar_query(self, req, rp):
        self.rp = rp
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
                    output['type'] = 'VEVENT'
                elif filter.get('name') == 'VTODO':
                    output['type'] = 'VTODO'
                    # Nothing to implement for the moment. Bongo doesn't support tasks.
                    pass


                # Loop through different ranges and whatnot.
                for f in filter.getchildren():
                    if normalize(f.tag) == 'urn:ietf:params:xml:ns:caldav:time-range':
                        # C:time-range -- only events between two times.
                        output['start'] = parse(f.get('start'))
                        output['end'] = parse(f.get('end'))

                    elif normalize(f.tag) == 'urn:ietf:params:xml:ns:caldav:is-defined':
                        # This means nothing in Bongo as all events in the store are "defined".
                        pass

        # Generate <D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
        self.multistatus_tag = ET.Element('D:multistatus') 
        bongo.commonweb.ElementTree.set_prefixes(self.multistatus_tag, {'D' : 'DAV:',
                                                                    'C' : 'urn:ietf:params:xml:ns:caldav'
                                                                })

        if output['type'] == 'VEVENT':
            ret = self._vevent_filter(output, prop_tags)
            if ret != bongo.commonweb.HTTP_OK:
                return ret

        # Throw out the output.
        req.content_type = 'text/xml; charset="utf-8"'
        req.write("""<?xml version="1.0" encoding="utf-8" ?>""")
        req.write(ET.tostring(self.multistatus_tag))
        return bongo.commonweb.HTTP_MULTI_STATUS

    ## The default entry point for the handler.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def do_REPORT(self, req, rp):
        self.store = StoreClient(req.user, rp.user, authPassword=req.get_basic_auth_pw())
        return {
            'urn:ietf:params:xml:ns:caldav:calendar-query' : self.calendar_query
        }[normalize(rp.xml_input.tag)](req, rp)
