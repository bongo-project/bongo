import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowWebservices(req):
    tmpl = Template.Template('tmpl/webservices.tmpl')
    tmpl.SendPage(req)
    return 0
