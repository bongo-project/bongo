import bongo.commonweb
import bongo.commonweb.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler
#from tarfile import TarInfo
#import datetime

class AliasHandler(HawkeyeHandler):
    def index_GET(self, req, rp):
        #today = datetime.date.today()
        #self.SetVariable("set", today.strftime("%y%m%d"))
        self.SetVariable("systab", "selecteditem")
        return self.SendTemplate(req, rp, "index.tpl", title="Aliases")

    def index_POST(self, req, rp):
        return bongo.commonweb.HTTP_UNAUTHORIZED

    def _getFile(self, req, filename):
        store = None
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            configfile = store.Read(filename)
            store.Quit()
        except:
            if store: 
                store.Quit()
            return None
        
        return configfile
