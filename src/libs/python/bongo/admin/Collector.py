import Handler, Mdb, Security, Template, Util
from bongo import MDB

def ShowCollector(req):
    tmpl = Template.Template('tmpl/collector.tmpl')
    tmpl.SendPage(req)
    return 0
