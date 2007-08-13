## Sundial PROPFIND request method handler.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb
import bongo.commonweb.ElementTree
from SundialHandler import SundialHandler
from bongo.store.StoreClient import StoreClient
from bongo.store.StoreClient import DocTypes
from libbongo.libs import cal, bongojson
import cElementTree as ET
import md5

## Class to handle PROPFIND request methods.
class PropfindHandler(SundialHandler):
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

    ## Get some information about the requested resource, be that a calendar, or event.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    #  @param store StoreClient instance.
    def _handle_resource(self, req, rp, store):
        is_calendar = False
        icsdata = ''

        if rp.filename != None:
            # Talking about an individual event, not a calendar.
            try:
                doc = store.Read(rp.fileuid)
                jsob = bongojson.ParseString(doc.strip())

                # Get the iCal data as that's what Sundial's etags are made from.
                icsdata = cal.JsonToIcal(jsob)
            except:
                raise HttpError(404)

        else:
            # Talking about a calendar, not a revolution.
            try:
                info = store.Info("/calendars/" + rp.calendar)
                if info.type == DocTypes.Calendar:
                    is_calendar = True
            except:
                raise HttpError(404)

        # is_calendar is a boolean, whether the request is for a calendar.
        # icsdata is a string. It contains the event data in iCal format.
        return [is_calendar, icsdata]

    ## The entry point for the handler.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def do_PROPFIND(self, req, rp):
        store = StoreClient(req.user, rp.user, authPassword=req.get_basic_auth_pw())

        # Get some information about what's being asked for.
        is_calendar, icsdata = self._handle_resource(req, rp, store)

        multistatus_tag = ET.Element('D:multistatus') # <D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
        bongo.commonweb.ElementTree.set_prefixes(multistatus_tag, { 'D' : 'DAV:',
                                                                    'C' : 'urn:ietf:params:xml:ns:caldav'
                                                                })
        response_tag = ET.SubElement(multistatus_tag, 'D:response') # <D:response>

        href_tag = ET.SubElement(response_tag, 'D:href')
        href_tag.text = rp.get_hostname() + "dav/" + rp.user + "/" + rp.calendar + "/" # <D:href>href</D:href>
        if not is_calendar:
            href_tag.text += rp.filename

        propstat_tag = ET.SubElement(response_tag, 'D:propstat') # <D:propstat>
        prop_tag = ET.SubElement(propstat_tag, 'D:prop') # <D:prop>

        for t in rp.xml_input.getchildren()[0].getchildren():
            if bongo.commonweb.ElementTree.normalize(t.tag) == 'dav::resourcetype':
                resourcetype_tag = ET.SubElement(prop_tag, 'D:resourcetype') # <D:resourcetype>
                # Check to make sure it is actually a calendar we're dealing with.
                # RFC2518 section 13.9 states the default type of D:resourcetype is empty, so only
                # return something if it is indeed a calendar.
                if is_calendar:
                    ET.SubElement(resourcetype_tag, 'C:calendar') # <C:calendar />

            elif bongo.commonweb.ElementTree.normalize(t.tag) == 'dav::getetag' and not is_calendar:
                ET.SubElement(prop_tag, 'D:getetag').text = '"%s"' % md5.new(icsdata.encode('ascii', 'replace')).hexdigest()

        ET.SubElement(propstat_tag, 'D:status').text = "HTTP/1.1 200 OK"

        req.headers_out['Content-Type'] = 'text/xml; charset="utf-8"'

        req.write("""<?xml version="1.0" encoding="utf-8" ?>""")
        req.write(ET.tostring(multistatus_tag))

        return bongo.commonweb.HTTP_MULTI_STATUS

