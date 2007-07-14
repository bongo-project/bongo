import cgi
from bongo.external import simplejson
import bongo.commonweb
from StringIO import StringIO
from bongo.store.StoreClient import StoreClient, DocFlags, DocTypes, FlagMode
from bongo import Xpl
import urlparse
import time
import random
import md5
import socket

class JSONWrapper:
    def __init__(self, str):
        self.str = str.strip()

class PassthroughEncoder(simplejson.JSONEncoder):
    def _iterencode(self, o, markers=None):
        if isinstance(o, JSONWrapper):
            return (o.str,)

        return simplejson.JSONEncoder._iterencode(self, o, markers)


class Template :
    """Lame little html template class"""
    def __init__(self, name, delimiter='|') :
        self.f = open(Xpl.DEFAULT_DATA_DIR + "/templates/" + name)
        self.delimiter = delimiter

    def send(self, req, getValue, getValueData) :        
        buf = self.f.read(8192)

        inToken = False
        leftover = None
        ifstack = []
        ignore = False
        while (buf) :
            split = buf.split(self.delimiter)

            if leftover :
                split[0] = leftover + split[0]
                leftover = None
            for i in range(0, len(split)):
                run = split[i]
                if inToken :
                    if i == len(split) - 1 and buf[-1] != self.delimiter :
                        # There might be more in the next buffer, save it
                        leftover = run                    
                    elif run == "" :
                        if not ignore :
                            req.write(self.delimiter)
                        inToken = False
                    else :
                        if run.startswith("ifdef:") :
                            key = run[6:]
                            value = getValue(req, key, getValueData)
                            if not value :
                                ifstack.append(True)
                                ignore = True
                            else :
                                ifstack.append(False)
                        elif run == "endif" :
                            ifstack.pop()
                            try :
                                ifstack.index(True)
                                ignore = True
                            except ValueError, e :
                                ignore = False
                        else :
                            value = getValue(req, run, getValueData)
                            if value and not ignore:
                                req.write(value)
                        inToken = False                        
                else :
                    if not ignore :
                        req.write(run)
                    inToken = True
            buf = self.f.read(8192)
        

class HttpHandler:
    def __init__(self):
        pass

    def _JsonToObj(self, str):
        return simplejson.loads(str)

    def _ObjToJson(self, obj):
        return simplejson.dumps(obj, cls=PassthroughEncoder)

    def _SendJson(self, req, jsob):
        return self._SendJsonText(req, self._ObjToJson(jsob))

    def _SendJsonText(self, req, data):
        req.headers_out["Content-Length"] = str(len(data))
        req.headers_out["Cache-Control"] = "no-cache"
        req.content_type = "text/javascript"

        req.write(data)

        return bongo.commonweb.OK

class ResourceHandler(HttpHandler):
    def _SystemTimezone(self):
        # FIXME: needs to be configurable
        return "/bongo/America/New_York"

    def GetStoreClient(self, req, rp):
        return StoreClient(req.user, rp.user, authToken=rp.authToken,
                           authCookie=req.authCookie)

    def ParsePath(self, rp) :
        pass

    def DocumentFlags(self, flags):
        ret = {}
        for flag in DocFlags.AllFlags():
            if flags & flag:
                ret[DocFlags.GetName(flag)] = True
        return ret

    def _SetDocumentFlags(self, store, info, flags):
        if (info.flags & flags) != flags:
            store.Flag(info.uid, flags, FlagMode.Add)

    def _ClearDocumentFlags(self, store, info, flags):
        if (info.flags & flags) != 0:
            store.Flag(info.uid, flags, FlagMode.Remove)


    def _GetUid (self, *args):
        t = long(time.time() * 1000)
        r = long(random.random() * 100000000000000000L)
        try:
            a = socket.gethostbyname(socket.gethostname())
        except:
            # if we can't get a network address, just imagine one
            a = random.random() * 100000000000000000L
        data = str(t) + ' ' + str (r) + ' ' + str(a) + ' ' + str(args)
        data = md5.md5(data).hexdigest()

        return data

    def _ServerHostname(self, req) :
        name = req.server.server_hostname
        split = name.split(':', 2)
        return split[0]

    def _BaseUrl(self, req) :
        if req.parsed_uri[0] :
            protocol = req.parsed_uri[0]
        else :
            # FIXME
            protocol = "http"
        return "%s://%s/user" % (protocol, req.server.server_hostname)

    def _LocalUrl(self, req, url) :
        '''Returns the local part of a given URL, or None if it is not a local url'''
        
        (scheme, hostname, path, parameters, query, fragment) = urlparse.urlparse(url)

        if hostname == req.server.server_hostname and path.startswith("/user/") :
            return path[6:]
        else :
            return None

    def _T(self, req, value) :
        # FIXME: actual translation
        return value

    def _FormatDate(self, req, date) :
        #FIXME: locale
        return date.strftime("%x")
        
    def _FormatTime(self, req, time) :
        #FIXME: locale
        return time.strftime("%I:%M%P")        

    def _GetTemplateValue(self, req, var, data=None) :
        if isinstance(data, dict) :
            value = data.get(var)
            if value :
                return value
        
        if var == "clientloc" :
            # FIXME
            return "/"
        if var.startswith("t:") :
            # Translated string
            return self._T(req, var[2:])
            
        return None
