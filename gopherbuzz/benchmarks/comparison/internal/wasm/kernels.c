// Freestanding WASM compute kernels for the wazero tier. No libc: math uses
// compiler builtins (sqrt -> wasm f64.sqrt). Each export mirrors the matching
// workload in comparison_test.go.

__attribute__((export_name("mandelbrot")))
int mandelbrot(void) {
  int checksum = 0;
  for (int py = 0; py < 150; py++) {
    for (int px = 0; px < 150; px++) {
      double x0 = px * 0.0125 - 1.5, y0 = py * 0.01 - 1.0;
      double zx = 0.0, zy = 0.0; int iter = 0;
      while (iter < 100 && zx*zx + zy*zy <= 4.0) {
        double tmp = zx*zx - zy*zy + x0; zy = 2.0*zx*zy + y0; zx = tmp; iter++;
      }
      checksum += iter;
    }
  }
  return checksum;
}

static long long a[80][80], b[80][80];
__attribute__((export_name("matmul")))
long long matmul(void) {
  int n = 80;
  for (int i = 0; i < n; i++)
    for (int j = 0; j < n; j++) { a[i][j] = i + j; b[i][j] = i - j; }
  long long trace = 0;
  for (int i = 0; i < n; i++)
    for (int k = 0; k < n; k++) {
      long long s = 0;
      for (int j = 0; j < n; j++) s += a[i][j] * b[j][k];
      if (i == k) trace += s;
    }
  return trace;
}

__attribute__((export_name("nbody")))
double nbody(void) {
  int n = 5;
  double x[5], y[5], z[5], vx[5], vy[5], vz[5], m[5];
  for (int i = 0; i < n; i++) { x[i]=i; y[i]=i*0.5; z[i]=i*0.25; vx[i]=0; vy[i]=0; vz[i]=0; m[i]=i+1; }
  double dt = 0.01;
  for (int step = 0; step < 10000; step++) {
    for (int i = 0; i < n; i++)
      for (int j = i+1; j < n; j++) {
        double dx=x[i]-x[j], dy=y[i]-y[j], dz=z[i]-z[j];
        double d2=dx*dx+dy*dy+dz*dz, dist=__builtin_sqrt(d2), mag=dt/(d2*dist);
        vx[i]-=dx*m[j]*mag; vy[i]-=dy*m[j]*mag; vz[i]-=dz*m[j]*mag;
        vx[j]+=dx*m[i]*mag; vy[j]+=dy*m[i]*mag; vz[j]+=dz*m[i]*mag;
      }
    for (int i = 0; i < n; i++) { x[i]+=vx[i]*dt; y[i]+=vy[i]*dt; z[i]+=vz[i]*dt; }
  }
  double e = 0.0;
  for (int i = 0; i < n; i++) e += 0.5*m[i]*(vx[i]*vx[i]+vy[i]*vy[i]+vz[i]*vz[i]);
  return e;
}
