import os
from bongo.Template import Template
import bongo.external.simplejson as simplejson
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.commonweb

class HawkeyeHandler:
    def __init__(self):
        self._template = Template()
        
    def GetStoreData(self, req, filename, storeName="_system"):
        config = {}
        store = None
        decoder = simplejson.JSONDecoder()
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store(storeName)
            configfile = store.Read(filename)
            config = decoder.decode(configfile)
            store.Quit()
        except ValueError, e:
            self.SetVariable("error", "Failed to parse JSON.")
            print "Failed to parse JSON: %s" % e
            if store: 
                store.Quit()
            return None
        except Exception, e:
            self.SetVariable("error", "Unable to read data from store: %s" % str(e))
            if store: 
                store.Quit()
            return None
        
        return config
        
    def SetStoreData(self, req, filename, data, storeName="_system"):
        encoder = simplejson.JSONEncoder()
        store = None
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store(storeName)
            configfile = encoder.encode(data)
            store.Replace(filename, configfile)
            if store:
                store.Quit()
            return True
        except ValueError:
            self.SetVariable("error", "Failed to parse JSON.")
            if store: 
                store.Quit()
            return False
        except Exception, e:
            self.SetVariable("error", "Unable to read data from store: %s" % str(e))
            if store: 
                store.Quit()
            return False
    
    def EventHistory(self, req, desc, path):
        config = {}
        store = None
        encoder = simplejson.JSONEncoder()
        decoder = simplejson.JSONDecoder()
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            
            # Fetch old data (if any)
            datafile = store.Read("/hawkeye/history")
            data = decoder.decode(datafile)
            
            # Now, move everything down one.
            data["4"] = data["3"]
            data["3"] = data["2"]
            data["2"] = data["1"]
            data["1"] = data["0"]
            
            # Put in new data in first slot
            data["0"] = { 'desc' : desc, 'url' : path }
            
            # Save new data
            datafile = encoder.encode(data)
            store.Replace("/hawkeye/history", datafile)
            
            # Finish up
            if store:
                store.Quit()
            return True
        except Exception, e:
            self.SetVariable("error", "Unable to set history: %s" % str(e))
            if store: 
                store.Quit()
            return False
    
    def NeedsAuth(self, rp):
        return True

    def ParsePath(self, rp):
        pass

    def SetVariable(self, name, value):
        self._template.setVariable(name, value)

    def index(self, req, rp):
        return bongo.commonweb.OK

    def SendTemplate(self, req, rp, file, title=None):
        if title != None:
            self.SetVariable("doctitle", "%s - Bongo Web Administration" % title)
        else:
            self.SetVariable("doctitle", "Bongo Web Administration")
            
        self.SetVariable("release", "M3")   # TODO: Set this from Makefile.
        path = os.path.join(rp.tmplPath, file)
        req.content_type = "text/html"

        if os.path.exists(path):
            print "sending template: %s / %s" % (rp.tmplPath, file)
            req.log.debug("sending template: %s", path)
        else:
            print "template does not exist: %s / %s " % (rp.tmplPath, file)
            req.log.debug("template does not exist: %s", path)
            req.write("<html><body><h1>Error</h1><p>Template not found.</p></body></html>")
            return bongo.commonweb.HTTP_NOT_FOUND

        self.SetVariable("message", "")
        try:
            t = self._template
            t.setTemplatePath(rp.tmplRoot)
            t.setTemplateUriRoot(rp.tmplUriRoot)
            t.setTemplateFile(path)
            t.Run(req)
            return bongo.commonweb.OK
        except ImportError, e:
            req.write("<html><h1>ERROR</h1><p>%s</p></body></html>" % str(e))
            return bongo.commonweb.HTTP_INTERNAL_SERVER_ERROR
        #except:
        #    print "Error running Template"
        #    req.write("<html><body><h1>Error</h1><p>Very bad error!</p></body></html>")
        #    return bongo.commonweb.HTTP_INTERNAL_SERVER_ERROR
