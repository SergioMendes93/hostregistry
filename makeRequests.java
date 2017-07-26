import java.net.*;
import java.util.*;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpHandler;
import com.sun.net.httpserver.HttpServer;
import java.net.URL;
import java.net.URLConnection;
import java.net.InetSocketAddress;
import java.util.ArrayList;
import java.util.List;
import java.io.*;
import redis.clients.jedis.Jedis; 
import redis.clients.jedis.Pipeline; 

public class makeRequests {

	public static void main(String[] args) throws Exception {
		HttpServer server = HttpServer.create(new InetSocketAddress(8001), 0);
        	server.createContext("/entrypoint", new MyHandler());
        	server.setExecutor(null); // creates a default executor
		server.start(); 
	}

	static class MyHandler implements HttpHandler {
 	   	//este metodo e quem trata dos requests 
        	@Override
        	public void handle(HttpExchange t) throws IOException {     
            	//cada request recebido sera tratado por uma thread diferente, passamos como parametro, o request recebido
           		Thread thread = new MyThread(t);
          		thread.start();         
        	}
	}

	static class MyThread extends Thread {
		String portNumber, image, makespan, ip;
		HttpExchange t;
		String query;
		long memory;
		int requestRate;
		long twogb = new Long("2147483648");

		public MyThread(HttpExchange t)
	    	{
    			this.t = t;
    			query = t.getRequestURI().getQuery(); //guardamos o que o user introduziu em string
    			if(query != null) {
				String[] parts = query.split("&"); 
				portNumber = parts[0];		
				image = parts[1];
				memory = new Long(parts[2]);
				makespan = parts[3];
				ip = parts[4];

				 if (memory < 536870912)
	                                requestRate =  20;
        	                else if (memory < 1073741824)
                	                requestRate =  40;
                        	else if (memory < twogb)
                                	requestRate =  80;
                        	else 
                                	requestRate = 160;

    			}
		}

		@Override
		public void run() {
			if (query != null) {
				boolean keep = true;
                		while (keep) {
                        		for (int j = 0; j < requestRate; j++) {
                                		try {
							Thread.sleep(1000);
                               				if (image.equals("redis")) {
                                        			Jedis jedis = new Jedis(ip, Integer.parseInt(portNumber));
                                        			System.out.println("from : " + ip + jedis.smembers("key1"));
		                                        	System.out.println(jedis.exists("key10000000000000")); 
                		                	} else {
									//http://10.5.60.3:10001/timeserver

                                                		String url = "http://"+ ip + ":" + portNumber + "/timeserver";
                                               			URL obj = new URL(url);
                                                		HttpURLConnection con = (HttpURLConnection) obj.openConnection();

                                               			int responseCode = con.getResponseCode();
                                                		//must return IP where the host was scheduled
                                                		BufferedReader in = new BufferedReader(new InputStreamReader(con.getInputStream())); 
                                                		String inputLine;
                                               			StringBuffer response = new StringBuffer();

                                                		while ((inputLine = in.readLine()) != null) {
                                                        		response.append(inputLine);
                                                		} 
                                                		System.out.println("Time from: " + ip + response);
							}
                                        	}catch(Exception e) {
							System.out.println("done at " + ip + " " + e);
                                                	keep = false;
                                                	break;
                                         	}
                                	}
                        		if (keep) {
                                		try {Thread.sleep(100000);}catch(Exception e){}
					}
                		}

			}
		}
	}	
}
