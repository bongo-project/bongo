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
import cElementTree as ET

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

    ## The entry point for the handler.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def do_PROPFIND(self, req, rp):
        store = StoreClient(req.user, rp.user, authPassword=req.get_basic_auth_pw())

        try:
            info = store.Info("/calendars/" + rp.calendar)
        except:
            return bongo.commonweb.HTTP_NOT_FOUND

        multistatus_tag = ET.Element('D:multistatus') # <D:multistatus xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
        bongo.commonweb.ElementTree.set_prefixes(multistatus_tag, { 'D' : 'DAV:',
                                                                    'C' : 'urn:ietf:params:xml:ns:caldav'
                                                                })
        response_tag = ET.SubElement(multistatus_tag, 'D:response') # <D:response>
        ET.SubElement(response_tag, 'D:href').text = rp.get_hostname() + "dav/" + rp.user + "/" + rp.calendar + "/" # <D:href>href</D:href>
        propstat_tag = ET.SubElement(response_tag, 'D:propstat') # <D:propstat>
        prop_tag = ET.SubElement(propstat_tag, 'D:prop') # <D:prop>


        for t in rp.xml_input.getchildren()[0].getchildren():
            if bongo.commonweb.ElementTree.normalize(t.tag) == 'dav::resourcetype':
                resourcetype_tag = ET.SubElement(prop_tag, 'D:resourcetype') # <D:resourcetype>
                # Check to make sure it is actually a calendar we're dealing with.
                # RFC2518 section 13.9 states the default type is empty, so only return something
                # if it is indeed a calendar.
                if info.type == DocTypes.Calendar:
                    ET.SubElement(resourcetype_tag, 'C:calendar') # <C:calendar />

        ET.SubElement(propstat_tag, 'D:status').text = "HTTP/1.1 200 OK"

        req.headers_out['Content-Type'] = 'text/xml; charset="utf-8"'

        req.write("""<?xml version="1.0" encoding="utf-8" ?>""")
        req.write(ET.tostring(multistatus_tag))

        return bongo.commonweb.HTTP_MULTI_STATUS

