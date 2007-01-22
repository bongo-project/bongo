import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowItip(req):
    tmpl = Template.Template('tmpl/itip.tmpl')
    tmpl.SendPage(req)
    return 0
