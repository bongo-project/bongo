import bongo.commonweb
import bongo.commonweb.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler

storePath = "/config/aliases"

class AliasHandler(HawkeyeHandler):
    def index_GET(self, req, rp):
        global storePath
        # Fetch list of current domains setup
        store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
        store.Store("_system")
        domains = store.List(storePath, ["nmap.document"])
        
        # Make listy
        ret = []
        for domain in domains:
            # Ignore magic .svn file for devel installs (workaround bug in bongo-config)
            if domain.filename.endswith("/.svn") != True:
                obj = { }
                obj["name"] = domain.filename.replace(storePath + "/", "")
                print "Domain was: %s" % domain.filename
                #print "Dir of domain: %s" % dir(domain)
                obj["deletelink"] = "delete/" + domain.uid
                obj["editlink"] = "edit/" + domain.uid
                ret.append(obj)
        
        # Send template foo.
        self.SetVariable("domainlist", ret)
        self.SetVariable("systab", "selecteditem")
        store.Quit()
        return self.SendTemplate(req, rp, "index.tpl", title="Domains")

    def index_POST(self, req, rp):
        return bongo.commonweb.HTTP_UNAUTHORIZED
        
    def edit_GET(self, req, rp):
        global storePath
        store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
        store.Store("_system")
        info = store.Info(rp.path)
        print "Doc info: %s" % info
        print "Actions on doc info: %s" % dir(info)
        
        # Template stuff
        domainName = info.filename.replace(storePath + "/", "")
        self.SetVariable("name", domainName)
        store.Quit()
        return self.SendTemplate(req, rp, "edit.tpl", title="Editing domain....")
    
    def delete_GET(self, req, rp):
        return self.SendTemplate(req, rp, "confirmdelete.tpl", title="Delete domain...")
