import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowAntispam(req):
    tmpl = Template.Template('tmpl/antispam.tmpl')
    tmpl.SendPage(req)
    return 0
