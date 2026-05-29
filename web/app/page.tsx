import "./marketing.css";
import TopBar from "@/components/marketing/TopBar";
import Hero from "@/components/marketing/Hero";
import MeshDemo from "@/components/marketing/MeshDemo";
import Features from "@/components/marketing/Features";
import HowItWorks from "@/components/marketing/HowItWorks";
import CodeShowcase from "@/components/marketing/CodeShowcase";
import CTA from "@/components/marketing/CTA";
import Footer from "@/components/marketing/Footer";

export default function Home() {
  return (
    <div className="rw-marketing">
      <TopBar />
      <main>
        <Hero />
        <MeshDemo />
        <Features />
        <HowItWorks />
        <CodeShowcase />
        <CTA />
      </main>
      <Footer />
    </div>
  );
}
