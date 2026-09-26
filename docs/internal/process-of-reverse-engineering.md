# Porting a library through reference tests and recorded behavior

A large part of this code was done via reverse engineering the ring application.  

The way this works is as follows: 
1. download an APK version of your app
2. download android studio/adb and run an instance of the phone
3. unpin the SSL certificates on the APK, and resign the APK
4. install a mitmproxy and run it
5. run the adb instanced emulator and run it with the proxied traffic over mitmproxy
6. run the app and make it work
7. capture network traffic as it goes

## gotchas
1. mitmproxy can capture websocket/http traffic, but it can't catch out of band traffic like a webRTC connection that does signalling over said connectioin. 
2. some applications have their own cert manager implementations, and work independently, so you have to be careful to watch SSL rejections in mitmproxy and wire in your own. 

## general reverse engineering tools
1. frida apk injector
2. jadx decompiler
3. adb android device studio
4. mitm proxy

