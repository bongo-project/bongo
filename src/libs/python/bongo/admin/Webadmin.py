import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowWebadmin(req):
    tmpl = Template.Template('tmpl/webadmin.tmpl')
    tmpl.SendPage(req)
    return 0
