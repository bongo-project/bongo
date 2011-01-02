import logging, time, os, sys, resource

from bongo.BongoError import BongoError
from libbongo.libs import bongojson, msgapi
from bongo.store.StoreClient import DocTypes, StoreClient

class Agent:
    log = logging.getLogger("Bongo.Error")
    running = True
    store = None
    
    def __init__(self, name='Undefined'):
        self.name = name
        self.log.debug("Starting agent '%s'" % (name))
    
    def connect(self, user, store):
        self.store = StoreClient(user, store, authPassword="bongo", eventCallback=self.callback)
    
    def callback(self, response):
        # we want to override this in the subclass
        self.log.error("Callback received and not handled")
        pass
    
    # without a proper event system in the store, we
    # essentially have to poll. FIXME.
    def waitForEvents(self):
        while self.running:
            self.store.NoOp()
            time.sleep(5)
        
        self.store.Quit()
    
    def daemonize(self):
        try:
            pid = os.fork()
        except OSError, e:
            raise Exception, e.strerror
        if (pid != 0):
            os._exit(0)
        
        os.setsid()
        
        try:
            pid = os.fork()
        except OSError, e:
            raise Exception, e.strerror
        if (pid != 0):
            os._exit(0)
        
        os.chdir("/")
        os.umask(0)
        
        maxFileDescriptor = resource.getrlimit(resource.RLIMIT_NOFILE)[1]
        if (maxFileDescriptor == resource.RLIM_INFINITY):
            maxFileDescriptor = 1024

        for fd in range(0, maxFileDescriptor):
            try:
                os.close(fd)
            except:
                pass
        
        devnull = "/dev/null"
        if (hasattr(os, "devnull")):
            devnull = os.devnull
        
        os.open(devnull, os.O_RDWR)
        os.dup2(0, 1)
        os.dup2(0, 2)

